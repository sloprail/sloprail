package main

import (
	"errors"

	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// Where a cycle measures its difference from, and how that point is kept
// honest.
//
// Two rules, and they only work as a pair.
//
// The point is recorded ONCE, at session start, and not revised as the session
// goes on. That is what keeps a file which failed a hook and was left unfixed
// inside the difference rather than dropping out of it — an agent that commits
// its unfixed work would otherwise move the point past its own violation.
//
// The one exception is the branch. An agent may switch branches mid-session,
// and a point recorded on the line it left describes a history the tree no
// longer has: the difference against it is every commit between the two, an
// entire branch delta arriving at one cycle as though this session had written
// it. So when the branch changes, and only then, the point is taken again.
//
// Moving it is safe only because an unfixed refusal does not depend on it. A
// failing verdict is kept in file_checks in its own right, keyed by path and
// guardrail, and is reported again on every cycle until a hook passes it —
// whatever point the difference is measured from. Both halves have to hold
// together, or a branch switch quietly drops a violation.

// baselineOutcome says what a call to the baseline did, so a caller can report
// it and a test can assert on it without reading the store back.
type baselineOutcome int

const (
	// baselineUnchanged: a point was already recorded on the line the tree is
	// still on. The ordinary case on every cycle after the first.
	baselineUnchanged baselineOutcome = iota

	// baselineRecorded: there was no point, and one was taken. The ordinary
	// case at session start.
	baselineRecorded

	// baselineMoved: the tree is on a different line of history than the point
	// was taken on, so a new point was taken. What keeps a branch switch from
	// delivering an entire branch delta to the next cycle.
	baselineMoved

	// baselineUnavailable: there is nothing to record — the tree is not a
	// repository, or is one with no commit yet. Not a failure: a project
	// without git is a project the engine guards with everything except the
	// difference, rather than one it refuses to start in.
	baselineUnavailable
)

// ensureBaseline records where the session measures from, and re-takes the
// point if the tree has moved to another line of history.
//
// Called at session start to establish the point, and at the end of every cycle
// to notice a switch. The two are the same call because the rule is the same
// one: the point is whatever describes the line the tree is on now, and it is
// only ever re-taken when that line has changed.
//
// A branch that cannot be read is not a branch that changed. Every failure path
// leaves the recorded point exactly as it was, because the alternative —
// treating an unreadable branch as a different one — would move the point on a
// transient git failure, and the whole reason the point holds still is so that
// unfixed work stays inside the difference.
func ensureBaseline(store sessionstate.Store, dir string) (baselineOutcome, error) {
	pos, err := gitrepo.Head(dir)
	if err != nil {
		if errors.Is(err, gitrepo.ErrNotARepository) {
			return baselineUnavailable, nil
		}
		return baselineUnavailable, err
	}
	if pos.Commit == "" {
		// A repository with no commit yet. Nothing to measure from until there
		// is one, and the next cycle asks again.
		return baselineUnavailable, nil
	}

	recordedBranch, hadBranch, err := store.Meta(sessionstate.MetaBaselineBranch)
	if err != nil {
		return baselineUnavailable, err
	}
	_, hadCommit, err := store.Meta(sessionstate.MetaBaselineCommit)
	if err != nil {
		return baselineUnavailable, err
	}

	switch {
	case !hadCommit || !hadBranch:
		// No point yet. The first cycle in a session, or the first one after
		// the tree became a repository.
		if err := writeBaseline(store, pos); err != nil {
			return baselineUnavailable, err
		}
		return baselineRecorded, nil

	case recordedBranch != pos.Branch:
		// The tree is on another line of history. The recorded commit
		// describes a history this tree no longer has, so the difference
		// against it would be the whole delta between the two branches.
		//
		// The unfixed refusals recorded so far are untouched by this: they sit
		// in file_checks keyed by path and guardrail, not against the point,
		// and keep surfacing until a hook passes them.
		if err := writeBaseline(store, pos); err != nil {
			return baselineUnavailable, err
		}
		return baselineMoved, nil

	default:
		// Same line of history. The point stays where it is even though HEAD
		// may have moved along that line — an agent that commits mid-session
		// must not push its own work out of the difference, which is exactly
		// what following HEAD would do.
		return baselineUnchanged, nil
	}
}

// writeBaseline records the point and the line it was taken on.
//
// The commit is written first. Neither order is atomic across two rows, and if
// only one lands the next cycle must not read a commit that is trusted against
// the wrong branch name: with the commit written and the branch missing,
// ensureBaseline sees an incomplete point and takes a fresh one. Written the
// other way round, a stale commit would sit under a current branch and be
// believed.
func writeBaseline(store sessionstate.Store, pos gitrepo.Position) error {
	if err := store.SetMeta(sessionstate.MetaBaselineCommit, pos.Commit); err != nil {
		return err
	}
	return store.SetMeta(sessionstate.MetaBaselineBranch, pos.Branch)
}
