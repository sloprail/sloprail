package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/tagmod"
)

// This file holds the shared end-of-cycle event helpers: how a completed cycle's
// Post events are gathered from the tree difference and from the agent's own
// messages. They were written for the old GUARDRAIL.md Post dispatch and outlive
// it — the new nature Stop dispatch (nature_stop.go) computes its Post events the
// same way, so a file-guard's after-checks and a context's Post enters see the
// same difference, re-fire and all, that the old format saw.

// postEvents gathers the Post file events for a completed cycle: the tree
// difference against this session's baseline, with every still-unfixed file
// re-added so a prior refusal re-fires. It returns the events and the repository
// root their paths resolve against.
func postEvents(cmd *cobra.Command, store sessionstate.Store, p HookPayload, reg *module.Registry, bound []string) ([]event.Event, string) {
	commit, ok, err := store.Meta(sessionstate.MetaBaselineCommit)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: baseline not read:", err)
		return nil, ""
	}
	if !ok || commit == "" {
		// No point to measure from — a project without git, or one with no
		// commit yet. The cycle still ends and Stop still fires; there is
		// simply no difference to report.
		return nil, ""
	}

	// The difference is taken FIRST and the problem reported after, the same
	// way the module's own events are taken below.
	//
	// A difference that came back is a difference worth dispatching even when
	// something in it could not be read: git may have named one path with a
	// status this engine does not know, and the other ninety-nine are still
	// real changes that rules are bound to. Throwing them away would turn one
	// unclassifiable file into a whole cycle judged by nothing — a silence
	// larger than the one it was reporting.
	diff, err := newTreeDifference(p.Cwd, commit)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: tree not fully compared:", err)
	}
	if diff == nil {
		return nil, ""
	}

	// What is still unfixed, whatever the tree now says.
	//
	// `refusal_outlives_baseline`, and the half the difference cannot supply. A
	// refused file leaves the difference as soon as the point moves past it —
	// the agent commits its own unfixed work, or switches to a line of history
	// that already holds the same content — and from that cycle on the tree is
	// silent about a file that is still broken. Read from the record instead,
	// which is the only thing that still remembers.
	//
	// Added BEFORE the emptiness check, because the case this exists for is
	// exactly a cycle whose difference is empty: nothing changed, and a
	// violation is nonetheless outstanding.
	readdOutstanding(cmd, store, diff)

	if diff.empty() {
		return nil, ""
	}

	in := module.Input{
		module.InputPhase:   module.PhasePost,
		module.InputPayload: diff,
	}

	var events []event.Event
	for _, m := range reg.Needed(bound) {
		evs, err := m.Extract(in)
		// The events are taken FIRST, and the error reported after.
		//
		// A module returns both together by contract, and the error describes
		// the paths it could not classify rather than the ones it could.
		//
		// This used to add that the pre-tool caller did the opposite, printing
		// and `continue`ing past the events. It no longer does — both callers
		// take the events first — and the claim is removed rather than left to
		// mislead the next reader into thinking one half of the engine still
		// drops ninety-nine correct classifications because one path would not
		// stat. See session_pre_tool.go, and the test that holds it there.
		events = append(events, evs...)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: module %q: %v\n", m.Name(), err)
		}
	}
	return events, diff.Root()
}

// tagEvents runs the tag module over this cycle's agent messages, when something
// binds to PostTagWrite.
//
// Separate from postEvents because its input is the RECORD, not the tree
// difference, and its event must fire even when the difference is empty — a
// cycle that wrote a tag in prose and touched no file. Folding it into postEvents
// would gate it on the diff and lose exactly that case.
//
// Gated on the bound kinds, the same discipline reg.Needed enforces for every
// other module: reading the whole transcript and scanning it for tokens is not
// free, and a project with no rule bound to PostTagWrite should pay nothing for
// the fact that tags can be scanned. The scan is skipped entirely when nothing
// asks — so the transcript is not even read.
//
// The messages are gathered and handed to the module through module.InputMessages;
// the module does the scanning. Errors gathering the messages are reported and
// swallowed inside cycleAgentMessages, which yields an empty list rather than
// failing — a truthful "no tags seen" for a cycle whose text could not be read.
func tagEvents(cmd *cobra.Command, store sessionstate.Store, p HookPayload, reg *module.Registry, bound []string) []event.Event {
	m, ok := reg.Lookup(tagmod.KindPostTagWrite)
	if !ok {
		// No module owns the kind — an impossible state in this build, since
		// tagmod is registered, but handled rather than assumed.
		return nil
	}
	if !boundTo(bound, tagmod.KindPostTagWrite) {
		// Nothing binds to it, so the scan is work done to be discarded — and its
		// cost is a whole-transcript read. Skip it, transcript included.
		return nil
	}

	in := module.Input{
		module.InputPhase:    module.PhasePost,
		module.InputMessages: cycleAgentMessages(cmd, store, p),
	}
	evs, err := m.Extract(in)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: module %q: %v\n", m.Name(), err)
	}
	return evs
}

// boundTo reports whether a kind is among those something in this project binds
// to. A small helper so the tag scan can be gated on its own kind the way
// reg.Needed gates the extractors it runs.
func boundTo(bound []string, kind string) bool {
	for _, k := range bound {
		if k == kind {
			return true
		}
	}
	return false
}

// readdOutstanding puts every still-unfixed file back into the difference.
//
// The reader `refusal_outlives_baseline` turns on. Retention alone does not
// enforce the invariant: a refusal sitting in the record changes nothing unless
// something asks for it, and until this existed the only reader was Skippable,
// which answers false for a refused file and for an unjudged one alike. So a
// refused file that left the difference was never put in front of its rule
// again — still broken, and nothing left to say so.
//
// Re-added rather than re-refused from the record. The verdict is not replayed
// here, and deliberately: the file is handed back to the rule that objected and
// judged again, so a violation the agent has since fixed outside the diff
// clears itself, and one it has not is refused afresh by the rule rather than
// by a cached answer. The dispatcher's own skip check is what keeps that from
// being wasteful — content already judged at this fingerprint is skipped, and a
// refusal never licenses a skip, so the hook re-runs exactly on what is unfixed.
//
// A path git already reported is left alone; see include.
//
// Failure is reported and swallowed, the way every other read in this
// bookkeeping is. The cost is a cycle that does not re-report an unfixed file,
// which is the same cost the engine paid on every cycle before this existed;
// refusing the agent's work because the record would not open is a refusal no
// guardrail asked for.
func readdOutstanding(cmd *cobra.Command, store sessionstate.Store, diff *treeDifference) {
	outstanding, err := store.OutstandingRefusals()
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: unfixed violations not re-checked:", err)
		return
	}
	for _, r := range outstanding {
		diff.include(r.Path)
	}
}
