package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/module/modules"
	"github.com/sloprail/sloprail/internal/natures"
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

A file-guard judges commits. Its range runs from a base to HEAD, and the base is
the first of these that exists and is still an ancestor of HEAD:

  watermark      the last head the rule passed, at its current definition
  folder floor   the last commit that touched the rule's folder (a rule that
                 lives in this repository)
  session start  the HEAD recorded when this session began (a plugin's rule, or
                 one not committed yet)

If none of them can be used — the session start was never recorded, or the tree
left its history — the command fails rather than guess.

--rule names the file-guard: its folder name (` + "`size-limit`" + `), or its qualified
name as a refusal cites it (` + "`file-guard/size-limit`" + `, ` + "`plugin/file-guard/size-limit`" + `).

The output is JSON on stdout:

  rule                the qualified name
  origin              which base was used: watermark | floor | session-start
  base, head          the range, as SHAs
  droppedWatermark    a watermark that was offered and is no longer reachable
                      (an amend or a rebase), when there was one
  ruleHash            the hash of the rule's whole folder
  unresolvedCitations Sloprail-Cites-* trailers whose quote did not resolve
  payload             what a check receives on stdin: event, changeset (commits,
                      files, others, citations), subject

Nothing is run and nothing is recorded. Where the session cannot be found from
the environment (no CLAUDE_CODE_SESSION_ID), there is no watermark and no
session start, and quotes are not resolved; the folder floor still applies.`,
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

	recordPath, store, err := openChangesetSession(&p)
	if err != nil {
		return err
	}
	if store != nil {
		defer store.Close()
	}

	hash, err := changeset.RuleHash(g.Dir)
	if err != nil {
		return err
	}
	rule := g.Qualified()
	var watermark, sessionStart string
	if store != nil {
		if watermark, _, err = store.Watermark(rule, hash); err != nil {
			return err
		}
		if sessionStart, _, err = store.Meta(sessionstate.MetaBaselineCommit); err != nil {
			return err
		}
	}
	r, err := gitrepo.ResolveRange(root, repoRelative(root, g.Dir), watermark, sessionStart)
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
	if recordPath != "" {
		project := projectDirOf(recordPath, p.Cwd)
		resolve := func(req transcript.CitationRequest) (transcript.Citation, error) {
			return transcript.ResolveCitationAcrossSessions(recordPath, project, req)
		}
		var missed []changeset.Unresolved
		cs.Citations, missed = changeset.ResolveCitations(cs.Commits, resolve)
		for _, u := range missed {
			unresolved = append(unresolved, u.String())
		}
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

// openChangesetSession finds the current session's record and its EXISTING
// store. It never creates a store: showing a changeset records nothing, so a
// session that has no state yet simply has no watermark and no session start.
// A missing session is not an error — the folder floor needs neither.
func openChangesetSession(p *HookPayload) (record string, store sessionstate.Store, err error) {
	record, err = p.record()
	if err != nil {
		return "", nil, err
	}
	if record == "" {
		record = transcript.CurrentSessionPath(p.Cwd)
	}
	if record == "" {
		return "", nil, nil
	}
	p.TranscriptPath = record
	id, err := stableID(*p)
	if err != nil {
		return record, nil, err
	}
	path, err := sessionDBPath(p.Cwd, id)
	if err != nil {
		return record, nil, err
	}
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return record, nil, nil
		}
		return record, nil, statErr
	}
	store, err = sessionstate.Open(path)
	return record, store, err
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
