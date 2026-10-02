package main

import (
	"encoding/json"
	"fmt"
	"os"
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
// A file-guard reads commits: the range merge-base(--base, --head)..--head as one
// squashed diff, with the commits' trailers and the quotes they cite. That is a lot to
// get right in a rule's match, script and rubric, and none of it is visible until a
// check refuses. This prints exactly what the engine would hand the rule — the range,
// and the Changeset payload — and runs nothing: no check, no judge, no verdict stored.
func newSessionChangesetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "changeset --rule <name> --base <rev> --head <rev>",
		Short: "What a file-guard would be judged on: its range and the Changeset payload, without running any check",
		Long: `Show what a file-guard would be judged on, without judging it.

A file-guard judges commits. Its range is merge-base(--base, --head)..--head, both
revisions stated by the caller (a branch, a tag or a sha).

--rule names the file-guard: its folder name (` + "`size-limit`" + `), or its qualified
name as a refusal cites it (` + "`file-guard/size-limit`" + `, ` + "`plugin/file-guard/size-limit`" + `).

The output is JSON on stdout:

  rule                the qualified name
  base, head          the range, as SHAs
  ruleHash            the hash of the rule's whole .sloprail root (its own folder,
                      every other rule, schemas and shared scripts)
  unresolvedCitations Sloprail-Cites-* trailers whose quote did not resolve
  payload             what a check receives on stdin: event, changeset (commits,
                      files, others, citations — each with the commits that carried
                      it and the files they changed), subject

Nothing is run and nothing is recorded. Where the session cannot be found from
the environment (no CLAUDE_CODE_SESSION_ID), quotes are not resolved.`,
		Args: cobra.NoArgs,
		RunE: runSessionChangeset,
	}
	cmd.Flags().String("rule", "", "The file-guard, by folder name or qualified name")
	_ = cmd.MarkFlagRequired("rule")
	addRangeFlags(cmd)
	return cmd
}

// changesetOutput is what `sr-session changeset` prints.
type changesetOutput struct {
	Rule                string            `json:"rule"`
	Base                string            `json:"base"`
	Head                string            `json:"head"`
	RuleHash            string            `json:"ruleHash"`
	UnresolvedCitations []string          `json:"unresolvedCitations"`
	Payload             changeset.Payload `json:"payload"`
}

func runSessionChangeset(cmd *cobra.Command, _ []string) error {
	name, _ := cmd.Flags().GetString("rule")
	baseRev, _ := cmd.Flags().GetString("base")
	headRev, _ := cmd.Flags().GetString("head")

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
	r, err := gitrepo.ResolveRange(root, baseRev, headRev)
	if err != nil {
		return fmt.Errorf("sloprail: %w", err)
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

	recordPath, err := p.record()
	if err != nil {
		return err
	}
	if recordPath == "" {
		recordPath = transcript.CurrentSessionPath(p.Cwd)
	}

	hash, err := changeset.RuleHash(g.Root())
	if err != nil {
		return err
	}
	ctx := contextMatchValue(contextsOf(cmd, nil, loaded.Contexts))
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
		Rule: g.Qualified(), Base: r.Base, Head: r.Head, RuleHash: hash, UnresolvedCitations: unresolved,
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
