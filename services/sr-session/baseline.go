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
// The point is recorded ONCE, where the session starts — before its first tool
// call at the latest (ensureBaselineRecorded) — and not revised as the session
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
// Moving it is safe because nothing that must outlive it depends on it. What a
// file-guard concluded lives in the check results, keyed by the rule and the
// commit range it judged — not against this point — so a branch switch cannot
// drop an unfixed violation. This point only decides which files a CONTEXT's Post
// events are computed over.

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
// re-taken when the tree can no longer reach it. Session start usually cannot
// reach the store at all — a fresh session has no transcript yet — which is why
// every tool call also asks, through ensureBaselineRecorded.
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
		// A repository with no commit yet. Nothing to measure a difference from until
		// there is one, and the next cycle asks again. But the session DID begin here:
		// recorded now, so the commits of its first turn are not mistaken for where it
		// began when the start is next taken (a file-guard's range starts at the empty tree).
		if _, had, err := store.Meta(sessionstate.MetaSessionStart); err != nil {
			return baselineUnavailable, err
		} else if !had {
			if err := store.SetMeta(sessionstate.MetaSessionStart, sessionstate.SessionStartUnborn); err != nil {
				return baselineUnavailable, err
			}
		}
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
	// A file-guard's unfixed refusals are untouched by this: a refusal is a recorded
	// run that did not pass, so the rule's watermark does not move past it, and the
	// range it refused stays the rule's range (changeset_range.go) until a run
	// passes. They never depended on this point. What does is the floor of last
	// resort — the HEAD the session began at, for a rule with no committed
	// definition — which is why the point still moves when the tree leaves its
	// history.
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

// ensureBaselineRecorded takes the point if the session has none yet, and
// otherwise does nothing at all. It is what a tool call runs, before the tool.
//
// # Why a tool call takes the point
//
// Session start cannot. Claude Code writes a session's transcript AFTER its
// SessionStart hooks have run, and the record the conversation's identity is
// read from (StableSessionID: the first record with no parent) is, in a fresh
// session, the attachment recording that very hook's result. So at startup the
// identity the store is keyed by does not exist yet, by construction rather
// than by a race. Measured across every real transcript on one machine: all 940
// `SessionStart:startup` runs of `sr-session start` failed to open the record,
// and in all 940 the hook's own attachment was the transcript's origin.
//
// Left to the end of the cycle, the first point was taken at the first Stop —
// AFTER the agent's first turn. An agent that committed its work during that
// turn then had its own final commit recorded as where the session began, the
// difference came back empty, and no file-guard ever saw the change. Found in a
// real evaluation run, where exactly that happened and the Stop hook judged
// nothing in 204ms.
//
// A tool call is the earliest moment that works, and it is early enough. The
// record exists by then — the prompt and the assistant turn that asked for the
// tool are written, and across the same real transcripts no PreToolUse run of
// `sr-session pre-tool` ever failed to open it. And nothing the agent does can
// have moved HEAD yet: a commit, a checkout, a reset all need a tool call, and
// every tool call reaches PreToolUse before it runs. So the first call records
// the commit the session actually began on.
//
// The same holds for a sub-agent. Claude Code reports agent_id on a sub-agent's
// tool events, so its PreToolUse resolves the sub-agent's own record and store
// (HookPayload.record), and its first tool call records where IT began — rather
// than its SubagentStop, which is after the only cycle most sub-agents have.
//
// # Why this does not also move the point
//
// Taking the point once is all a tool call needs to do. Noticing that the tree
// LEFT the recorded history is ensureBaseline's other job, and it stays at the
// end of the cycle, where the difference is actually measured: a point that
// moves mid-cycle and one that moves at Stop yield the same difference at Stop.
// Keeping it out of here keeps this path to two reads of the already-open store
// on every call after the first — no git process at all — and leaves one place,
// the one that announces it, where the point ever moves.
func ensureBaselineRecorded(store sessionstate.Store, dir string) (baselineOutcome, error) {
	_, hadCommit, err := store.Meta(sessionstate.MetaBaselineCommit)
	if err != nil {
		return baselineUnavailable, err
	}
	_, hadBranch, err := store.Meta(sessionstate.MetaBaselineBranch)
	if err != nil {
		return baselineUnavailable, err
	}
	if hadCommit && hadBranch {
		return baselineUnchanged, nil
	}
	// No point, or half of one (see writeBaseline on the order): ensureBaseline
	// decides both the same way, by taking a fresh one.
	return ensureBaseline(store, dir)
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
	// The session's first start is kept for a file-guard's range and never moved;
	// see sessionstate.MetaSessionStart.
	if _, had, err := store.Meta(sessionstate.MetaSessionStart); err != nil {
		return err
	} else if !had {
		if err := store.SetMeta(sessionstate.MetaSessionStart, pos.Commit); err != nil {
			return err
		}
	}
	if err := store.SetMeta(sessionstate.MetaBaselineCommit, pos.Commit); err != nil {
		return err
	}
	return store.SetMeta(sessionstate.MetaBaselineBranch, pos.Branch)
}
