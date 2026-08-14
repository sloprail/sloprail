package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newSessionSubagentStopCmd is the hook point that fires when a SUB-AGENT's work
// cycle ends.
//
// A separate command rather than a branch inside stop, and the reason is the
// whole design. The harness fires two different events, and each fires in
// exactly one place: Stop in the root agent only, SubagentStop in the sub-agent
// only. So which agent is ending is not something to work out — the invocation
// already said it. A single command that had to decide would be re-deriving,
// from evidence, a fact it was handed.
//
// Deriving it from the records instead would be answering a settled question
// with the wrong kind of evidence. A transcript marks a sub-agent's records with
// isSidechain, and it is tempting to read that field — but the same file is read
// by the sub-agent's own hook here AND by the parent's hook at its own Stop, so
// nothing in its contents distinguishes which of the two is calling. The
// discriminator is the invocation, and it is exact.
//
// Not, to be clear, because roots are full of sidechain records: they are not.
// Of the 8,119 main transcripts measured on one machine, zero contained one,
// including all 89 that demonstrably dispatched a sub-agent. An earlier version
// of this comment asserted the opposite as the reason to avoid the field, and
// was simply wrong on the fact. The conclusion survives its own bad argument —
// what makes sniffing wrong is that it re-derives from a mutable file layout
// something the caller was handed outright. Being a separate command is what
// makes that mistake unavailable rather than merely discouraged.
//
// Without this, a sub-agent's cycle ends with no guardrail running at all. Stop
// fires only in the root, so before this command existed the plugin bound
// nothing to the moment a sub-agent finishes: work delegated to a sub-agent was
// simply not guarded, and the root's own Stop would later see the sub-agent's
// changes attributed to the root — if it shared the tree, and not at all if it
// did not.
//
// What it does with the cycle is deliberately the same as stop's, because a
// sub-agent is a session in every sense that matters here: the tree is compared
// against where THIS session started and the guardrails bound to what changed
// are run. The difference is entirely in which session that is, and that is
// settled before this command does anything, by record() choosing the
// sub-agent's own transcript and stableID resolving its own identity from it.
//
// The tree-diffing and event dispatch are stubbed here exactly as they are in
// stop, and for the same reason: that machinery is being built on other
// branches. What is real here is the routing — which session this is, and
// therefore which state anything it records belongs to.
func newSessionSubagentStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "subagent-stop",
		Short: "End of a SUB-AGENT's cycle: run the guardrails bound to what it changed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := readPayload(cmd)
			if p.StopHookActive {
				// Already refused once this cycle. Refusing again would be a
				// loop the sub-agent cannot leave — and a sub-agent has less
				// recourse than a root, since the person is not watching it.
				return nil
			}

			// Never act as the wrong session. A payload reaching this command
			// names a sub-agent by definition, so one that does not is a harness
			// whose contract has changed — and running the cycle anyway would
			// run it under the DISPATCHING session's identity, writing a
			// sub-agent's verdicts into the root's state. That is the exact
			// confusion this command exists to prevent.
			//
			// Reported and abandoned rather than returned as an error, and the
			// distinction is load-bearing at this hook point. A non-zero exit
			// from SubagentStop is a BLOCK: the harness re-runs the sub-agent's
			// turn, and a condition that will not change on a retry makes that a
			// loop the sub-agent cannot leave — it would turn "sloprail cannot
			// tell whose cycle this is" into "this sub-agent can never finish".
			// Declining to judge is the safe failure; refusing to let work
			// complete is not, and a guardrail engine that bricks delegation is
			// worse than one that stays quiet about a cycle it could not place.
			//
			// It is not silent, but it is quiet, and the difference is worth
			// knowing. The reason goes to stderr, which reaches a person tailing
			// logs; measured against this harness, a SubagentStop hook that
			// exits zero has neither stream forwarded into the run's own
			// output. The only channel from here that the session can see is a
			// non-zero exit — which is the block this branch exists to avoid. So
			// the gap is visible where gaps are looked for, and invisible to the
			// sub-agent, which is the right way round.
			if !p.IsSubagent() {
				fmt.Fprintln(cmd.ErrOrStderr(),
					"sloprail: subagent-stop was invoked without a sub-agent on the payload — not judging this cycle rather than judging it as the session that dispatched it")
				return nil
			}

			// Resolved for its effect: which session this is decides which state
			// anything recorded below belongs to. A sub-agent whose own record
			// cannot be read is one whose cycle cannot be placed, which is again
			// a reason to stand down rather than to trap it in a retry.
			if _, err := stableID(p); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"sloprail: subagent-stop could not identify this sub-agent's session, so its cycle went unjudged: %v\n", err)
				return nil
			}

			return nil // TODO: diff the tree, dispatch the Post events
		},
	}
}
