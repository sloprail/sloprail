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
// The one exception is the tree LEAVING the history the point sits in. An agent
// may switch to another line mid-session, and a point recorded on the line it
// left describes a history the tree no longer has: the difference against it is
// every commit between the two, an entire branch delta arriving at one cycle as
// though this session had written it.
//
// What "leaving" means is reachability, not the branch name. The name is the
// wrong question in both directions, and asking it was the original bug:
//
//   - The name can change while the history does not. `git checkout -b feature`
//     and `git branch -m` both keep HEAD exactly where it is; re-taking the
//     point on either would push the session's own commits out of the
//     difference through the branch door, which is the very thing the
//     record-once rule exists to prevent.
//   - The history can change while the name does not. Two unrelated detached
//     checkouts both report no branch at all, so comparing names would call
//     them the same place and leave the point on a commit the tree has left.
//
// So the question asked is whether the recorded commit is still reachable from
// HEAD. Reachable means everything between it and HEAD arrived along the way
// and belongs inside the difference. Unreachable means the point describes a
// history this tree no longer has, and it is taken again.
//
// Moving it is safe only because an unfixed refusal does not depend on it. A
// failing verdict is kept in file_checks in its own right, keyed by path,
// guardrail and the content it was reached on, and is reported again on every
// cycle until a hook passes it — whatever point the difference is measured
// from. Both halves have to hold together, or a branch switch quietly drops a
// violation.
//
// KEEPING the verdict is only half of that, and the quieter half is reading it
// back: a refusal nothing asks for enforces nothing. Once the point moves, the
// refused file is no longer a difference and the tree says nothing about it, so
// the re-reporting comes from the record instead — OutstandingRefusals, read by
// readdOutstanding at the end of every cycle. See T015_04.

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

	// baselineMoved: the tree has left the history the point was taken in, so a
	// new point was taken. What keeps a branch switch from delivering an entire
	// branch delta to the next cycle.
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
// to notice the tree leaving. The two are the same call because the rule is the
// same one: the point is a commit the tree can still reach, and it is only ever
// re-taken when the tree can no longer reach it.
//
// A history that cannot be read is not a history that changed. Every failure
// path leaves the recorded point exactly as it was, because the alternative —
// treating an unreadable state as a different one — would move the point on a
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
	recordedCommit, hadCommit, err := store.Meta(sessionstate.MetaBaselineCommit)
	if err != nil {
		return baselineUnavailable, err
	}

	if !hadCommit || !hadBranch {
		// No point yet. The first cycle in a session, or the first one after
		// the tree became a repository.
		if err := writeBaseline(store, pos); err != nil {
			return baselineUnavailable, err
		}
		return baselineRecorded, nil
	}

	if recordedCommit == pos.Commit {
		// The tree has not moved at all. Nothing to decide, and worth answering
		// before asking git anything: this is the common case on a cycle that
		// changed no history, and it is the case where a branch RENAME lands —
		// same commit, same history, a different name, and no reason to move.
		return baselineUnchanged, nil
	}

	// The tree moved. Whether it moved ALONG the recorded history or LEFT it is
	// the only thing that matters, and only reachability answers that.
	stillReachable, err := gitrepo.Contains(dir, recordedCommit)
	if err != nil {
		return baselineUnavailable, err
	}
	if stillReachable {
		// Moved along the same history. The point stays where it is — an agent
		// that commits mid-session must not push its own work out of the
		// difference, which is exactly what following HEAD would do. This is
		// also where `git checkout -b` lands: a new branch off the session's
		// own commits reaches all of them, so nothing has been left.
		return baselineUnchanged, nil
	}

	// The tree has left the history the point sits in. The recorded commit
	// describes a history this tree no longer has, so the difference against it
	// would be the whole delta between the two.
	//
	// The unfixed refusals recorded so far are untouched by this: they sit in
	// file_checks keyed by path, guardrail and content, not against the point,
	// and keep surfacing until a hook passes them — readdOutstanding puts them
	// back into the difference on every cycle, which is what makes that true
	// once the point has moved past them.
	//
	// One exception, and it is the reason Position.Branch still exists. A rebase
	// walks a detached HEAD through commits that reach nothing recorded, at
	// every step. Left to reachability alone the point would be re-taken several
	// times over a single rebase, each time landing on a commit the operation is
	// about to replace. So while an operation is RUNNING on the branch the point
	// was taken on, the point waits for it to finish.
	//
	// Only while it is running. A rebase rewrites the commits it walks, so the
	// moment it finishes the branch reads exactly as it did before while the
	// history under it has been replaced — and the recorded point is on a commit
	// the rebase discarded. Holding on the name alone would keep it there for
	// the rest of the session. Position.Operation is what separates "still
	// moving" from "settled", and once it is settled the question above is asked
	// properly and the point moves if the history really did.
	if pos.Operation && pos.Branch == recordedBranch {
		return baselineUnchanged, nil
	}

	if err := writeBaseline(store, pos); err != nil {
		return baselineUnavailable, err
	}
	return baselineMoved, nil
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
