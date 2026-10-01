package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/gitrepo"
)

// outstandingRefusalBases is where a rule's range must reach back to so that nothing an
// EARLIER session of this worktree was refused for, and never fixed, falls out of
// sight. A rule that existed at session start is judged from the session start, which is
// after what an earlier session was refused for; without this a refusal is lost the
// moment a second session starts, and the file stays broken on disk with nobody
// judging it.
//
// Read from the sibling sessions' check stores — the other `<session>/checks.db`
// beside this one under the same workspace directory (the worktree's key) — read-only.
// A refusal is outstanding while its head is still an ancestor of HEAD and no later
// pass, in any of those stores or this one, is at a descendant of that head. History
// that passed or was never checked is not here, and stays grandfathered. A store that
// cannot be read is an error: a range that cannot be known fails closed.
func outstandingRefusalBases(root, rule string, own checkstore.Store) ([]string, error) {
	if own == nil || filepath.Base(own.Path()) != "checks.db" || !filepath.IsAbs(own.Path()) {
		return nil, nil
	}
	ownPath := own.Path()
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(ownPath)), "*", "checks.db"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)

	type ref = checkstore.RunRef
	var failed, passed []ref
	mine, err := own.RunRefs(rule)
	if err != nil {
		return nil, err
	}
	passed = append(passed, mine.Passed...)
	for _, p := range paths {
		if p == ownPath {
			continue
		}
		refs, err := readSibling(p, rule)
		if err != nil {
			return nil, fmt.Errorf("the check results of another session of this worktree (%s) could not be read: %w", p, err)
		}
		failed = append(failed, refs.Failed...)
		passed = append(passed, refs.Passed...)
	}

	var bases []string
	var every []string
	for _, f := range failed {
		every = append(every, f.Head)
	}
	for _, p := range passed {
		every = append(every, p.Head)
	}
	graph := gitrepo.LoadGraph(root, every...)
	headSha := ""
	if h, herr := gitrepo.Head(root); herr == nil {
		headSha = h.Commit
	}
	for _, f := range failed {
		var reach bool
		var err error
		if headSha != "" {
			reach, err = gitrepo.IsAncestorFast(root, graph, f.Head, headSha)
		} else {
			reach, err = gitrepo.Contains(root, f.Head)
		}
		if err != nil {
			return nil, err
		}
		if !reach {
			continue // rewritten away: not something this tree still holds
		}
		var later []string
		for _, p := range passed {
			if p.RunAt > f.RunAt {
				later = append(later, p.Head)
			}
		}
		fixed, err := gitrepo.AnyDescendant(root, graph, f.Head, later)
		if err != nil {
			return nil, err
		}
		if !fixed {
			bases = append(bases, f.Base)
		}
	}
	return bases, nil
}

func readSibling(path, rule string) (checkstore.RunRefs, error) {
	s, err := checkstore.OpenReadOnly(path)
	if errors.Is(err, checkstore.ErrNoStore) {
		return checkstore.RunRefs{}, nil
	}
	if err != nil {
		return checkstore.RunRefs{}, err
	}
	defer s.Close()
	return s.RunRefs(rule)
}
