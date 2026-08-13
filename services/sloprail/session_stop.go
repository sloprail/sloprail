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

	// Where this cycle's reading ended, for the next one to resume after. Only
	// on this path: a cycle that was interrupted may have judged nothing, and
	// moving the mark anyway skips whatever it never looked at.
	advanceReadMark(cmd, store, p)

	return nil // TODO: diff the tree, dispatch the Post events
}

// advanceReadMark records how far this session's record has been read.
//
// The mark is the uuid of the last entry in the record as it stands now, which
// is what the cycle that just finished was able to see. The next cycle resumes
// after it.
//
// An empty record leaves the previous mark standing rather than clearing it.
// Writing an empty mark would mean "nothing has been read", and the next cycle
// would read the session from its beginning — safe, but it discards a position
// that was correct, and every subsequent cycle would re-judge everything before
// it.
//
// Failure is reported and swallowed. The cost of not moving the mark is that
// the next cycle re-reads some turns; the cost of refusing here is the agent's
// work blocked over the engine's bookkeeping.
func advanceReadMark(cmd *cobra.Command, store sessionstate.Store, p HookPayload) {
	path, err := p.record()
	if err != nil || path == "" {
		return
	}
	entries, err := transcript.Read(path)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: read mark not advanced:", err)
		return
	}
	mark := transcript.Mark(entries)
	if mark == "" {
		return
	}
	if err := store.SetMeta(sessionstate.MetaTranscriptRead, mark); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: read mark not advanced:", err)
	}
}
