package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/module/modules"
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
			// A retry after a refusal (stop_hook_active) is judged like any other
			// Stop: a reply does not pass a rule by being sent twice. What ends a
			// refusal loop is the project's stop_hook_block_cap — see
			// stopHookBlockCapReached.
			return completeCycle(cmd, readPayload(cmd))
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
		// The engine's own bookkeeping is unavailable, which is no reason to skip
		// judging: the file-guards need only git and the check results. Run the Stop
		// dispatch without a store (gate and context state in memory for this cycle),
		// and say so in the refusal if it refuses. Never a silent pass.
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: session state unavailable at Stop, judging without it:", err)
		if p.Cwd == "" {
			// No folder named: there is nothing to judge, and guessing the process's own
			// directory would judge somebody else's tree. Plumbing, never a block.
			fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: the payload names no working directory, so there is no folder to judge")
			return nil
		}
		if reason := natureStopDispatch(cmd, p); reason != "" {
			note := fmt.Sprintf("\n(sloprail's session state could not be opened, so this Stop was judged without it and the refusal-loop cap does not apply: %v)", err)
			if berr := block(cmd, reason+note); berr != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", berr)
			}
		}
		return nil
	}
	defer store.Close()

	// The refusal loop's escape valve, checked before anything else so a Stop
	// let through un-judged changes nothing: the mark does not move past turns
	// nothing judged, the point does not move (an outstanding refusal is work
	// that must stay inside the next difference), and only the read position is
	// discarded — see discardOffered.
	if stopHookBlockCapReached(cmd, store, p) {
		discardOffered(cmd, p)
		return nil
	}

	// The tree may have moved to another line of history during the cycle. If
	// it did, the recorded point describes a history it no longer has and the
	// next difference would be the whole delta between the two branches.
	//
	// A sub-agent's baseline is normally taken as its tool calls begin. When it
	// arrives at its OWN Stop with none, the point taken now is the HEAD its
	// commits already produced — fine for a context's next difference, but not a
	// place to measure a file-guard's range from: every range that needs it (a
	// rule with no folder floor, a plugin's) would be empty and pass unjudged. It
	// is marked as taken at Stop, and resolveRuleRange will not use it.
	_, hadBaseline, _ := store.Meta(sessionstate.MetaBaselineCommit)
	if outcome, err := ensureBaseline(store, p.Cwd); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: baseline not re-taken:", err)
	} else if outcome == baselineRecorded && p.IsSubagent() && !hadBaseline {
		if err := store.SetMeta(sessionstate.MetaBaselineAtStop, "1"); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
		}
	} else if outcome == baselineMoved {
		// The cited-change history before this cycle describes the line the
		// tree left; see pruneHistory.
		if err := pruneHistory(store, cycleStartedAt(store)); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
		}
		// Worth saying out loud. The next cycle's difference is measured from
		// somewhere other than where the session began, and a person reading
		// why a file stopped appearing in it should not have to infer this.
		fmt.Fprintln(cmd.ErrOrStderr(),
			"sloprail: the tree moved to another branch — measuring from a new point; recorded violations still stand")
	}

	// Diffing the tree and dispatching the cycle's Stop work belongs here, between
	// the baseline and the mark.
	//
	// The ORDER is the load-bearing part, not the placement. The mark says a
	// position has been judged, and it is only true once the events for that
	// position have been dispatched. Advance it before dispatch and the claim is
	// one nothing has earned: the position is recorded as judged by a cycle that
	// ran no judging at all.
	//
	// So the mark is held until the Stop dispatch has run: the nature Stop
	// dispatch runs the cycle's file-guard after-checks, context enters/exits and
	// Stop gates, and a refusal from any of them blocks the turn and sends the
	// agent round again over these same turns — which it cannot do if the cycle
	// has just declared them judged. On a refusal the mark is held below, exactly
	// as an objection used to hold it.
	//
	// What the mark loses by waiting is a re-read, not a turn. The position is
	// discarded with the cycle either way, so the next cycle reads from the mark
	// — which has not moved — and sees everything this one saw, plus whatever
	// arrived since. Carrying the position across instead would hand it to a
	// cycle that never read those turns; see discardOffered.
	if reason := natureStopDispatch(cmd, p); reason != "" {
		if err := block(cmd, reason); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
		}
		countStopRefusal(cmd, store)
		discardOffered(cmd, p)
		return nil
	}
	resetStopRefusals(cmd, store)

	// Where this cycle's reading ended, for the next one to resume after, and
	// then the position is spent. Only on this path: a cycle that was
	// interrupted may have judged nothing, and moving the mark anyway skips
	// whatever it never looked at.
	advanceReadMark(cmd, store, p)
	discardOffered(cmd, p)

	return nil
}

// natureStopDispatch runs the new-format Stop dispatch for a completed cycle,
// resolving the registry the module registry supplies. Returns the text to
// block the turn with, or "" to let it end. A variable so a test can stand it
// in.
var natureStopDispatch = func(cmd *cobra.Command, p HookPayload) string {
	reg, err := modules.Registry()
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
		return ""
	}
	return natureDispatchStop(cmd, p, reg)
}

// discardOffered forgets how far the record was read out, because the cycle
// that read it is over.
//
// F5, and the same defect the mark itself was built around: a position asserted
// by something that never earned it.
//
// MetaTranscriptOffered is written by `session query` and answers "how far did
// THIS cycle read". Left standing at the end of a cycle it stops being about
// this cycle and becomes a claim the next one inherits — and a later cycle that
// queries nothing then completes would take the mark from it, marking turns
// judged that it was never shown. Reachable in an ordinary session: a cycle
// reads and is interrupted, turns land, and the next cycle finishes without any
// rule having queried.
//
// So it is cleared on all three paths a cycle ends on: interrupted, blocked by a
// Stop refusal, and completed. What the first two lose is only a re-read — the
// mark did not move, so the next cycle reads from where it still is and is
// offered every turn this one saw. Re-reading a turn costs a second look;
// skipping one loses a violation for good, which is the direction this errs in.
//
// A fourth path exists and is deliberately not one of them: completeCycle
// returns early when the store cannot be opened at all. Nothing is reachable
// there, because clearing needs the same store that just failed to open — the
// stale position survives, and the next cycle inherits it. That is a real hole
// rather than a covered one, and it is the same hole every piece of this
// bookkeeping has when its own database is unavailable; the failure is already
// reported on stderr where it happens.
//
// Failure here is likewise reported and swallowed — with one thing worth naming,
// since it is a failure to UNDO. A clear that does not land leaves the stale
// position exactly where it was, so the defect above is live again on the next
// cycle. It is still not worth refusing the agent's work over: the cost is a
// re-read of turns a cycle did see rather than a turn skipped, and blocking here
// would refuse work for a reason no guardrail asked for.
func discardOffered(cmd *cobra.Command, p HookPayload) {
	store, err := openEngineState(p)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: read position not cleared:", err)
		return
	}
	defer store.Close()

	if err := store.SetMeta(sessionstate.MetaTranscriptOffered, ""); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: read position not cleared:", err)
	}
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
// That holds only because the position is discarded when a cycle ends. It is
// read here, not owned here: without the clearing it would carry over from an
// earlier cycle and this function would happily advance the mark on behalf of a
// cycle that queried nothing at all. See discardOffered.
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
// which carry no order of their own. What each combination of "present" and
// "absent" means is isBefore's business, and it is not the arithmetic on -1
// that an index comparison falls into by default — see there.
//
// F11. Reading the position, deciding against it, and writing is a
// read-modify-write, and the rules querying within one cycle are separate hook
// PROCESSES that may run at once — so two of them can both read the same
// position, both decide theirs is further, and the later write can land on a
// value its comparison never saw. Measured at 1 lost update in 200 concurrent
// runs, which left the position at the nearer of the two.
//
// The lost update lands BACKWARDS, so it costs a re-read rather than a skipped
// turn — the same direction everything in this bookkeeping errs in. It is fixed
// anyway because the claim was the problem: recordOffered says the position only
// ever moves forward, and a claim that is false under concurrency is worse than
// no claim, since the reasoning downstream of it is what stops turns going
// missing.
//
// So the decision and the write are one step against the database. A swap that
// loses means another query moved the position while this one was deciding, and
// the answer is to decide again against what is there now rather than to
// overwrite it: the winner may already be further on, in which case this read
// has nothing to add. Bounded because each retry has a winner, so the position
// advances every time round and a caller cannot spin against a position that
// keeps changing without it also keeps moving forward.
//
// F12. A position is only taken when it is the END of the record it was read
// from, which is the one thing that makes index order mean forward movement.
//
// isBefore reads "a sits at a lower index than b" as "b is further on", and that
// only follows while the record is append-only. Let the entries before the
// recorded position be REORDERED — recorded c, record rewritten to a,c,b,d, a
// later read naming b — and the index comparison says c is before b, the
// position moves to b, and the next cycle's Since returns [d]. The entry b was
// marked judged by nothing, which is the one direction this bookkeeping must
// never err in.
//
// Refusing a mid-record position closes it without having to detect the rewrite.
// The caller records transcript.Mark(whole) — the last entry of the record it
// just read — on every path, so this rejects nothing the engine actually does;
// what it rejects is a position that CANNOT have come from reading a record to
// its end. A transcript is append-only in normal operation, so this needs an
// external rewrite to reach at all: a guard rather than a live defect.
func advanceOffered(store sessionstate.Store, entries []transcript.Entry, offered string) error {
	if len(entries) > 0 && offered != transcript.Mark(entries) {
		// Not the end of this record, so index order says nothing about whether
		// it is further on than what is stored. Holding costs a re-read.
		return nil
	}
	for range offeredSwapAttempts {
		current, ok, err := store.Meta(sessionstate.MetaTranscriptOffered)
		if err != nil {
			return err
		}
		if ok && current != "" && !isBefore(entries, current, offered) {
			// Whatever is stored is at least as far on as this read. Nothing to
			// add, and nothing to race over.
			return nil
		}
		// Absent and empty are both "no position recorded", and both are matched
		// by an empty old — so the first write of the key and a write over a
		// cleared one take the same path.
		swapped, err := store.SwapMeta(sessionstate.MetaTranscriptOffered, current, offered)
		if err != nil {
			return err
		}
		if swapped {
			return nil
		}
		// Lost the swap: another query wrote between the read and the write. Its
		// value is what the next decision must be made against.
	}
	// Every attempt lost, which means the position moved forward on each one. The
	// mark is not wrong — it is somewhere a real read reached — so this is
	// reported rather than retried forever, and the caller swallows it the way it
	// swallows every other failure to record.
	return fmt.Errorf("sloprail: read position contended by other queries after %d attempts", offeredSwapAttempts)
}

// offeredSwapAttempts bounds the retry in advanceOffered. Every losing attempt
// means another query won and moved the position forward, so a handful is far
// past what the few rules querying within one cycle can produce.
const offeredSwapAttempts = 10

// isBefore reports whether the entry named by a comes strictly before the one
// named by b, as the record orders them — which is to say, whether replacing a
// with b moves the position forward.
//
// Four cases, and only one of them is an ordering question. Written out rather
// than left to `indexOf(a) < indexOf(b)`, which silently gives -1 an order it
// has not got and answers two of the other three wrongly:
//
//   - Both present: the record orders them, and that is the answer.
//   - Only a is present: b is a name this record cannot locate, so nothing says
//     it is further on. Holding still is the only safe answer — moving to it
//     would leave the position somewhere no later read can resume from.
//   - Only b is present: a is the unlocatable one, so the record it was taken
//     from was replaced or truncated. It is tempting to move — b is at least
//     somewhere this record has — and the doc here used to say so. But a record
//     that no longer holds a is a record that LOST turns, and nothing left in
//     it says where a sat, so b may well be behind it. That is the truncation
//     recordOffered promises not to be dragged backwards by, and it cannot be
//     told apart from a legitimate replacement by looking at this record. So
//     this holds, and the tie is broken by which way it is safe to be wrong:
//     an unlocatable position makes the next read start from the beginning of
//     the record, which costs a re-read, while a position dragged backwards
//     costs re-judging settled work and a position dragged forwards loses a
//     turn for good.
//   - Neither present: the record confirms nothing about either, so neither is
//     a position anything can resume from and there is no re-reading to save.
//     Naive index arithmetic says -1 < -1 is false and keeps the older one for
//     no reason; this cycle's own reading is the better of two names the record
//     cannot place.
//
// Every case that is not a plain ordering errs the same way: never forwards
// onto a turn nothing read, and never backwards onto work already judged.
func isBefore(entries []transcript.Entry, a, b string) bool {
	ia, ib := indexOf(entries, a), indexOf(entries, b)
	switch {
	case ia >= 0 && ib >= 0:
		return ia < ib
	case ia >= 0:
		// b is unlocatable and a works. Moving would give up a position a later
		// read can resume from for one it cannot.
		return false
	case ib >= 0:
		// a is unlocatable, b is not, and where a sat is unknowable from here.
		// Holding costs a re-read; moving risks going backwards.
		return false
	default:
		// Neither is locatable, so neither saves a re-read. The newer one is at
		// least this cycle's own reading.
		return true
	}
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
