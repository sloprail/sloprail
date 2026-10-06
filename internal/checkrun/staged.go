package checkrun

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"sync"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/guardrail"
)

// StagedParams is what StagedNeedingCitation reads: the file-guards and the repository whose
// index is the candidate change.
type StagedParams struct {
	// Err receives diagnostics; nil discards them.
	Err    io.Writer
	Guards []declaration.FileGuard
	Root   string
	// Amend: the candidate replaces HEAD, so it is judged against HEAD's parent.
	Amend bool
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
	var snapshot *gitrepo.Snapshot
	defer func() {
		if snapshot != nil {
			_ = snapshot.Remove()
		}
	}()

	// A merge needs a citation only for what its resolution changed: a file it took unchanged from
	// one side was changed by that side's commits, which carry their own citations. The commits
	// that changed a file include the merge only when it differs from what every parent had.
	merging, err := gitrepo.IsMerge(p.Root, rng.Head)
	if err != nil {
		return nil, err
	}
	// What is already on the remote default branch is not work to answer for: a commit that
	// restores a file to exactly the content it has at the merge base changes nothing a citation
	// could ground (a partial restore still differs, and still needs one).
	mergeBase, haveBase := gitrepo.RemoteDefaultBase(p.Root, rng.Head)
	var out []string
	for _, g := range guards {
		match, err := guardrail.CompileFileMatch(g.Match)
		if err != nil {
			return nil, fmt.Errorf("file-guard %q: its match %q could not be compiled: %w", g.Name, g.Match, err)
		}
		cs, err := changeset.Build(p.Root, rng, changeset.Options{
			Deletions: changeset.DeletionMode(g.Deletions),
			Scan:      Markers,
			Select:    Selector(match),
		})
		if err != nil {
			return nil, fmt.Errorf("file-guard %q: %w", g.Name, err)
		}
		for _, req := range g.Require {
			if req.Citation == nil {
				continue
			}
			var subs []changeset.Subject
			for _, s := range changeset.Subjects(cs, changeset.Requirement) {
				if merging && !changedBy(cs, s.ID, rng.Head) {
					continue
				}
				if haveBase && restoresBase(p.Root, mergeBase, cs, s.ID) {
					continue
				}
				if !slices.Contains(out, s.ID) {
					subs = append(subs, s)
				}
			}
			if len(subs) == 0 {
				continue
			}
			if req.When == "" {
				for _, s := range subs {
					out = append(out, s.ID)
				}
				continue
			}
			if snapshot == nil {
				if snapshot, err = gitrepo.AddSnapshot(p.Root, "", rng.Head); err != nil {
					return nil, err
				}
			}
			results := make([]bool, len(subs))
			errs := make([]error, len(subs))
			missed := make([]int, len(subs))
			for i := range subs {
				missed[i] = i
			}
			// A `when` script is run once per subject (its contract), so a change touching many files
			// runs it many times: those runs are independent, so they run side by side.
			var wg sync.WaitGroup
			sem := make(chan struct{}, whenWorkers())
			for _, i := range missed {
				wg.Add(1)
				sem <- struct{}{}
				go func(i int) {
					defer wg.Done()
					defer func() { <-sem }()
					payload := changeset.NewPayload(cs, subs[i], "")
					r := dispatchcore.Request{
						Nature: dispatchcore.NatureFileGuard, Dir: g.Dir, GuardName: g.Name, Changeset: &payload,
						ProjectRoot: snapshot.Path, Env: changeset.Env(snapshot.Path, rng.Base, rng.Head),
					}
					results[i], errs[i] = dispatchcore.Runner{}.PrerequisiteApplies(r, req)
				}(i)
			}
			wg.Wait()
			for _, i := range missed {
				if errs[i] != nil {
					return nil, fmt.Errorf("file-guard %q: %w", g.Name, errs[i])
				}
			}
			for i, s := range subs {
				if results[i] {
					out = append(out, s.ID)
				}
			}
		}
	}
	slices.Sort(out)
	return out, nil
}

// whenWorkers is how many `when` scripts run side by side. Verdicts are never kept between runs:
// nothing an agent can write may stand in for a script's answer.
func whenWorkers() int { return max(2, min(8, runtime.NumCPU())) }

// changedBy reports whether the commit is among those that changed the file.
func changedBy(cs changeset.Changeset, path, sha string) bool {
	for _, f := range cs.Files {
		if f.Path == path {
			return slices.Contains(f.Commits, sha)
		}
	}
	return false
}

// restoresBase reports whether the modified file's content in the changeset is byte-identical to
// what it is at the merge base. A rename, an addition, a deletion and anything unreadable is not.
func restoresBase(root, base string, cs changeset.Changeset, path string) bool {
	for _, f := range cs.Files {
		if f.Path != path {
			continue
		}
		if f.Status != "M" || f.NewBlob == "" {
			return false
		}
		want, err := gitrepo.BlobRaw(root, f.NewBlob)
		if err != nil {
			return false
		}
		got, err := gitrepo.BlobRaw(root, base+":"+path)
		return err == nil && got == want
	}
	return false
}
