package checkrun

import (
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/natures"
)

// StagedParams is what StagedNeedingCitation reads: the file-guards and the repository whose
// index is the candidate change.
type StagedParams struct {
	// Err receives diagnostics; nil discards them.
	Err    io.Writer
	Guards []declaration.FileGuard
	Root   string
	// Amend: the candidate replaces HEAD, so it is judged against HEAD's parent.
	Amend      bool
	ContextMap map[string]natures.ContextState
}

// StagedNeedingCitation is the staged files a commit would have to cite: those selected by a
// file-guard with a `require: citation` entry whose `when` (if any) applies to the file. It is
// the prevention half of what `sr-checks run` refuses after the commit, and uses the same
// selection (changeset.Build with the rule's match and deletions mode) and the same `when`
// evaluation (a script run once per file, on that file's own subject payload), over the
// candidate commit gitrepo.StagedRange makes of the index. Paths come back sorted, once each.
//
// The commit's own message is not known yet, so a `match` reading `trailers` sees none, and
// whether the commit carries a citation is the caller's question, not this one's.
// Any failure is an error: a change that could not be read is never one that needs nothing.
func StagedNeedingCitation(p StagedParams) ([]string, error) {
	errw := p.Err
	if errw == nil {
		errw = io.Discard
	}
	var guards []declaration.FileGuard
	for _, g := range p.Guards {
		if slices.ContainsFunc(g.Require, func(r declaration.Prerequisite) bool { return r.Citation != nil }) {
			guards = append(guards, g)
		}
	}
	if len(guards) == 0 {
		return nil, nil
	}
	rng, err := gitrepo.StagedRange(p.Root, p.Amend)
	if err != nil {
		return nil, err
	}
	// The candidate was read from the index named by GIT_INDEX_FILE (a caller's scratch copy, when
	// it replays what a command line stages first). What follows checks out a snapshot, and git
	// would write that checkout's index over the caller's file.
	os.Unsetenv("GIT_INDEX_FILE")
	ctx := ContextMatchValue(p.ContextMap)
	var snapshot *gitrepo.Snapshot
	defer func() {
		if snapshot != nil {
			_ = snapshot.Remove()
		}
	}()

	var out []string
	for _, g := range guards {
		match, err := guardrail.CompileFileMatch(g.Match)
		if err != nil {
			return nil, fmt.Errorf("file-guard %q: its match %q could not be compiled: %w", g.Name, g.Match, err)
		}
		cs, err := changeset.Build(p.Root, rng, changeset.Options{
			Deletions: changeset.DeletionMode(g.Deletions),
			Scan:      Markers,
			Select:    Selector(match, ctx),
		})
		if err != nil {
			return nil, fmt.Errorf("file-guard %q: %w", g.Name, err)
		}
		for _, req := range g.Require {
			if req.Citation == nil {
				continue
			}
			for _, s := range changeset.Subjects(cs, changeset.Requirement) {
				if slices.Contains(out, s.ID) {
					continue
				}
				if req.When != "" && snapshot == nil {
					if snapshot, err = gitrepo.AddSnapshot(p.Root, "", rng.Head); err != nil {
						return nil, err
					}
				}
				payload := changeset.NewPayload(cs, s, "", ctx)
				r := dispatchcore.Request{
					Nature: dispatchcore.NatureFileGuard, Dir: g.Dir, GuardName: g.Name, Changeset: &payload,
				}
				if snapshot != nil {
					r.ProjectRoot = snapshot.Path
					r.Env = changeset.Env(snapshot.Path, rng.Base, rng.Head)
				}
				applies, err := dispatchcore.Runner{}.PrerequisiteApplies(r, req)
				if err != nil {
					return nil, fmt.Errorf("file-guard %q: %w", g.Name, err)
				}
				if applies {
					out = append(out, s.ID)
				}
			}
		}
	}
	slices.Sort(out)
	return out, nil
}
