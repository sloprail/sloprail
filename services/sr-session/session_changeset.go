package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/module/modules"
	"github.com/sloprail/sloprail/internal/natures"
	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// newSessionChangesetCmd shows what a file-guard would be judged on, without
// judging it.
//
// A file-guard reads commits: the range from where it last passed to HEAD, as
// one squashed diff, with the commits' trailers and the quotes they cite. That
// is a lot to get right in a rule's match, script and rubric, and none of it is
// visible until Stop refuses. This prints exactly what the engine would hand the
// rule — the base and where it came from, the head, and the Changeset payload —
// and runs nothing: no check, no judge, no verdict recorded, no watermark moved.
func newSessionChangesetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "changeset --rule <name>",
		Short: "What a file-guard would be judged on: its range and the Changeset payload, without running any check",
		Long: `Show what a file-guard would be judged on, without judging it.

A file-guard judges commits. Its range runs from a base to HEAD, and the base is:

  watermark      the latest head the rule passed, at ANY definition of the rule,
                 while it is still an ancestor of HEAD (else its merge base with HEAD): work up
                 to it was approved
  otherwise
    root floor     the PARENT of the last commit that touched the rule's whole
                   .sloprail root (a rule that lives in this repository): the
                   commit that adds or changes a rule, a schema or a shared lib is
                   judged by it. A root commit's base is git's empty tree
    session start  the HEAD recorded when this session began (the only candidate
                   for a plugin's rule, whose root is outside this repository, or
                   one not committed yet)
  A rule that did NOT exist at session start (its folder is absent from that
  commit's tree) uses the root floor alone: it applies from the commit that added
  it, and earlier history is grandfathered. A rule that existed then (also one
  deleted and re-added; also when the start is unknown or unborn) uses the session
  start, never earlier: a rule changed long before the session does not re-judge
  what was merged since, and nothing made in this session is skipped.

The watermark is not stored on its own: it is the newest run of the rule that
passed and whose head is still an ancestor of HEAD, read from the session's check
results (` + "`sr-checks status`" + ` shows them).

A watermark or session start the tree left (an amend, a rebase, a reset) is not
skipped: it is re-anchored at its merge base with HEAD. A session start git no longer
has, or that shares no history with HEAD, falls to git's empty tree (everything is
judged, the root commit included); so does a session that began before the first commit.

If none can be used — no watermark, no floor, and no session start recorded — the
command fails rather than guess.

--rule names the file-guard: its folder name (` + "`size-limit`" + `), or its qualified
name as a refusal cites it (` + "`file-guard/size-limit`" + `, ` + "`plugin/file-guard/size-limit`" + `).

The output is JSON on stdout:

  rule                the qualified name
  origin              which base was used: watermark | floor | session-start
  base, head          the range, as SHAs
  droppedWatermark    a watermark that was offered and is no longer reachable
                      (an amend or a rebase), when there was one
  ruleHash            the hash of the rule's whole .sloprail root (its own folder,
                      every other rule, schemas and shared scripts)
  unresolvedCitations Sloprail-Cites-* trailers whose quote did not resolve
  payload             what a check receives on stdin: event, changeset (commits,
                      files, others, citations — each with the commits that carried
                      it and the files they changed), subject

Nothing is run and nothing is recorded. Where the session cannot be found from
the environment (no CLAUDE_CODE_SESSION_ID), there is no watermark and no
session start, and quotes are not resolved; the root floor still applies.`,
		Args: cobra.NoArgs,
		RunE: runSessionChangeset,
	}
	cmd.Flags().String("rule", "", "The file-guard, by folder name or qualified name")
	_ = cmd.MarkFlagRequired("rule")
	return cmd
}

// changesetOutput is what `sr-session changeset` prints.
type changesetOutput struct {
	Rule                string            `json:"rule"`
	Origin              string            `json:"origin"`
	Base                string            `json:"base"`
	Head                string            `json:"head"`
	DroppedWatermark    string            `json:"droppedWatermark,omitempty"`
	RuleHash            string            `json:"ruleHash"`
	UnresolvedCitations []string          `json:"unresolvedCitations"`
	Payload             changeset.Payload `json:"payload"`
}

func runSessionChangeset(cmd *cobra.Command, _ []string) error {
	name, _ := cmd.Flags().GetString("rule")

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("sloprail: working directory: %w", err)
	}
	p := readPayloadIfWaiting(cmd)
	if p.Cwd == "" {
		p.Cwd = cwd
	}
	root, err := gitrepo.Root(p.Cwd)
	if err != nil {
		return fmt.Errorf("sloprail: a changeset is a range of commits: %w", err)
	}

	reg, err := modules.Registry()
	if err != nil {
		return fmt.Errorf("sloprail: %w", err)
	}
	loaded := newNatureDeclarations(cmd, p.Cwd, reg)
	g, err := findFileGuard(loaded, name)
	if err != nil {
		return err
	}
	match, err := guardrail.CompileFileMatch(g.Match)
	if err != nil {
		return fmt.Errorf("sloprail: file-guard %q match %q does not compile: %w", g.Name, g.Match, err)
	}

	sess, err := openChangesetSession(&p)
	if err != nil {
		return err
	}
	defer sess.close()
	recordPath, store := sess.record, sess.state

	hash, err := changeset.RuleHash(g.Root())
	if err != nil {
		return err
	}
	rule := g.Qualified()
	r, err := resolveRuleRangeIn(root, g, sess.checks, store, sessionFolderFor(p, root))
	if err != nil {
		return fmt.Errorf("sloprail: file-guard %q: range not computable: %w", g.Name, err)
	}

	ctx := contextMatchValue(contextsOf(cmd, store, loaded.Contexts))
	cs, err := changeset.Build(root, r, changeset.Options{
		Deletions: changeset.DeletionMode(g.Deletions),
		Scan:      changesetMarkers,
		Select:    changesetSelector(match, ctx),
	})
	if err != nil {
		return fmt.Errorf("sloprail: file-guard %q: %w", g.Name, err)
	}

	unresolved := []string{}
	for _, u := range resolveChangesetCitations(&cs, recordPath, p.Cwd) {
		unresolved = append(unresolved, u.String())
	}

	out := changesetOutput{
		Rule: rule, Origin: string(r.Origin), Base: r.Base, Head: r.Head,
		DroppedWatermark: r.DroppedWatermark, RuleHash: hash, UnresolvedCitations: unresolved,
		Payload: changeset.NewPayload(cs, changeset.Whole(cs), recordPath, ctx),
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// findFileGuard picks the loaded file-guard a --rule value names: by folder name
// or by qualified name. Ambiguity is an error, not a first-wins.
func findFileGuard(loaded declaration.Loaded, name string) (declaration.FileGuard, error) {
	var hits []declaration.FileGuard
	for _, g := range loaded.FileGuards {
		if g.Name == name || g.Qualified() == name {
			hits = append(hits, g)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		var have []string
		for _, g := range loaded.FileGuards {
			have = append(have, g.Qualified())
		}
		return declaration.FileGuard{}, fmt.Errorf("sloprail: no file-guard %q is loaded (loaded: %s)", name, strings.Join(have, ", "))
	default:
		return declaration.FileGuard{}, fmt.Errorf("sloprail: %q names %d file-guards; use the qualified name", name, len(hits))
	}
}

// repoRelative is dir's path relative to the repository root, or "" when dir is
// not inside it — a plugin's rule lives in the plugin cache, and has no folder
// in this repository for a floor to be found from.
func repoRelative(root, dir string) string {
	abs, err := filepath.EvalSymlinks(dir)
	if err != nil {
		abs = dir
	}
	top, err := filepath.EvalSymlinks(root)
	if err != nil {
		top = root
	}
	rel, err := filepath.Rel(top, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(rel)
}

// changesetSession is what `changeset` reads of the current session: its
// record, its state, and its check results. Every part may be absent — a
// session that has recorded nothing yet has no state and no results.
type changesetSession struct {
	record string
	state  sessionstate.Store
	checks checkstore.Store
}

func (c changesetSession) close() {
	if c.state != nil {
		c.state.Close()
	}
	if c.checks != nil {
		c.checks.Close()
	}
}

// openChangesetSession finds the current session's record and its EXISTING
// stores. It never creates one: showing a changeset records nothing, so a
// session with no state yet simply has no watermark and no session start. A
// missing session is not an error — the folder floor needs neither.
func openChangesetSession(p *HookPayload) (changesetSession, error) {
	var sess changesetSession
	record, err := p.record()
	if err != nil {
		return sess, err
	}
	if record == "" {
		record = transcript.CurrentSessionPath(p.Cwd)
	}
	if record == "" {
		return sess, nil
	}
	sess.record = record
	p.TranscriptPath = record
	id, err := stableID(*p)
	if err != nil {
		return sess, err
	}
	statePath, err := sessionDBPath(p.Cwd, id)
	if err != nil {
		return sess, err
	}
	if _, statErr := os.Stat(statePath); statErr == nil {
		if sess.state, err = sessionstate.Open(statePath); err != nil {
			return sess, err
		}
	} else if !os.IsNotExist(statErr) {
		return sess, statErr
	}
	checksPath, err := sessionpath.ChecksDB(p.Cwd, id)
	if err != nil {
		sess.close()
		return changesetSession{}, err
	}
	switch sess.checks, err = checkstore.OpenReadOnly(checksPath); {
	case errors.Is(err, checkstore.ErrNoStore):
		sess.checks = nil
	case err != nil:
		sess.close()
		return changesetSession{}, err
	}
	return sess, nil
}

// contextsOf is the declared contexts' state: read from the store when there is
// one, every declared context inactive when there is not.
func contextsOf(cmd *cobra.Command, store sessionstate.Store, contexts []declaration.Context) map[string]natures.ContextState {
	if store != nil {
		return loadContextMap(cmd, store, contexts)
	}
	out := map[string]natures.ContextState{}
	for _, c := range contexts {
		out[c.Name] = natures.ContextState{Payload: map[string]any{}}
	}
	return out
}
