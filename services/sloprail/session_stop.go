package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// newSessionStopCmd is the hook point that fires when a work cycle ends.
//
// What a cycle changed is established here by comparing the tree against where
// the session started, rather than by trusting what any action announced.
func newSessionStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "End of a cycle: run the guardrails bound to what it changed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := readPayload(cmd)
			if p.StopHookActive {
				// Already refused once this cycle. Refusing again would be a
				// loop the agent cannot leave.
				//
				// Nothing is recorded on this path, and that is deliberate on
				// both counts. The cycle did not finish, so the read mark must
				// not move past turns nothing judged. And the point stays where
				// it is, since a refusal outstanding is exactly the work that
				// must remain inside the next cycle's difference.
				return nil
			}
			return completeCycle(cmd, p)
		},
	}
}

// completeCycle is what a finished cycle leaves behind.
//
// Two pieces of bookkeeping, and they are here rather than at session start
// because both are facts about a cycle having ENDED: the tree may have moved to
// another line of history during it, and the session's record has been read up
// to a point that the next cycle should resume after.
//
// Neither can refuse the cycle. A failure to record is reported and the cycle
// ends anyway — the engine's own bookkeeping going wrong is not the project's
// rule being violated, and blocking on it would refuse the agent's work for a
// reason no guardrail asked for.
func completeCycle(cmd *cobra.Command, p HookPayload) error {
	store, err := openEngineState(p)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
		return nil
	}
	defer store.Close()

	// The tree may have moved to another line of history during the cycle. If
	// it did, the recorded point describes a history it no longer has and the
	// next difference would be the whole delta between the two branches.
	//
	// Any refusal recorded during this session survives this: a failing verdict
	// lives in file_checks in its own right, keyed by path and guardrail, and
	// is reported again on every cycle until a hook passes it — whatever point
	// the difference is measured from.
	if outcome, err := ensureBaseline(store, p.Cwd); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: baseline not re-taken:", err)
	} else if outcome == baselineMoved {
		// Worth saying out loud. The next cycle's difference is measured from
		// somewhere other than where the session began, and a person reading
		// why a file stopped appearing in it should not have to infer this.
		fmt.Fprintln(cmd.ErrOrStderr(),
			"sloprail: the tree moved to another branch — measuring from a new point; recorded violations still stand")
	}

	// Diffing the tree and dispatching the Post events belongs here, between the
	// baseline and the mark, and it does not exist yet.
	//
	// The ORDER is the load-bearing part, not the placement. The mark says a
	// position has been judged, and it is only true once the events for that
	// position have been dispatched. Advance it before dispatch exists and the
	// claim is one nothing has earned: the position is recorded as judged by a
	// cycle that ran no judging at all.
	//
	// So the mark is held until dispatch is a step that ran. dispatchPostEvents
	// reports whether it did, and today it reports that it did not, which stops
	// the mark rather than letting an ordering that is currently vacuous look
	// correct. What the mark loses by waiting is nothing: the position is kept
	// in MetaTranscriptOffered, only grows, and the cycle that finally dispatches
	// carries it forward for every cycle that could not.
	if !dispatchPostEvents(cmd, store, p) {
		return nil
	}

	// Where this cycle's reading ended, for the next one to resume after. Only
	// on this path: a cycle that was interrupted may have judged nothing, and
	// moving the mark anyway skips whatever it never looked at.
	advanceReadMark(cmd, store, p)

	return nil
}

// dispatchPostEvents runs the guardrails bound to what this cycle changed, and
// reports whether it ran at all.
//
// Not implemented. Owned by the work that diffs the tree and dispatches the
// events; this exists so the read mark has something real to wait on rather
// than a comment promising an order the code does not keep.
//
// Returning false is what holds the mark. A cycle that dispatched nothing has
// judged nothing, so it has no position to claim as judged — and the position
// it read is remembered elsewhere and lost by no one.
//
// A variable so a test about the mark's POSITION can stand this step in and
// still be testing the position rather than this step's absence. The tests that
// do keep meaning the same thing once this is implemented for real.
var dispatchPostEvents = func(_ *cobra.Command, _ sessionstate.Store, _ HookPayload) bool {
	// TODO: diff the tree against the baseline, dispatch the Post events, and
	// return true once a cycle's judging actually happens here.
	return false
}

// advanceReadMark carries forward the position this cycle actually read.
//
// The mark is NOT the end of the record as it stands now. It is the position
// `session query` recorded when it handed the record out, carried forward
// unchanged. Those are different positions whenever a turn was appended between
// the query and this moment, and the difference is a turn lost for good: marked
// judged without ever having been offered to anything, and never revisited,
// because a record only grows and the mark only moves forward.
//
// So nothing is re-read here. A cycle that queried nothing advances nothing,
// which is correct — a cycle that looked at no part of the record has judged no
// part of it, and the next cycle is owed everything.
//
// Failure is reported and swallowed. The cost of not moving the mark is that
// the next cycle re-reads some turns; the cost of refusing here is the agent's
// work blocked over the engine's bookkeeping.
func advanceReadMark(cmd *cobra.Command, store sessionstate.Store, p HookPayload) {
	offered, ok, err := store.Meta(sessionstate.MetaTranscriptOffered)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: read mark not advanced:", err)
		return
	}
	if !ok || offered == "" {
		// Nothing was read out during this cycle, so there is nothing this cycle
		// is entitled to call judged. The previous mark stands.
		return
	}
	if err := store.SetMeta(sessionstate.MetaTranscriptRead, offered); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: read mark not advanced:", err)
	}
}

// advanceOffered records how far the record has been read out, never letting
// the position go backwards.
//
// Several rules may query within one cycle, each seeing the record as it stood
// when it asked. The cycle as a whole saw the furthest of them, so the position
// keeps the furthest and ignores the rest.
//
// Order is decided by position in entries rather than by comparing the uuids,
// which carry no order of their own. A recorded position that is not in the
// record is treated as behind the new one: it names a place this record cannot
// confirm — the record it pointed into was replaced or truncated — and holding
// onto it would keep a position nothing can locate.
func advanceOffered(store sessionstate.Store, entries []transcript.Entry, offered string) error {
	current, ok, err := store.Meta(sessionstate.MetaTranscriptOffered)
	if err != nil {
		return err
	}
	if ok && current != "" && !isBefore(entries, current, offered) {
		return nil
	}
	return store.SetMeta(sessionstate.MetaTranscriptOffered, offered)
}

// isBefore reports whether the entry named by a comes strictly before the one
// named by b, as the record orders them. A name the record does not hold counts
// as before every name it does.
func isBefore(entries []transcript.Entry, a, b string) bool {
	ia, ib := indexOf(entries, a), indexOf(entries, b)
	return ia < ib
}

// indexOf is where a uuid sits in the record, or -1 when it is not there.
func indexOf(entries []transcript.Entry, uuid string) int {
	for i, e := range entries {
		if e.UUID == uuid {
			return i
		}
	}
	return -1
}
