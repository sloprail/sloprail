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
// same way, for the contexts' Post enters. A file-guard does not read them: it
// judges the commits of its own range (changeset_eval.go).

// postEvents gathers the Post file events for a completed cycle: the tree
// difference against this session's baseline. It returns the events and the
// repository root their paths resolve against.
func postEvents(cmd *cobra.Command, store sessionstate.Store, p HookPayload, reg *module.Registry, bound []string) ([]event.Event, string) {
	if replayInjected != nil { // a rule test: the cycle's events as the case gave them (replay_seam.go)
		return injectedOfKinds(replayInjected.post, bound), p.Root()
	}
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
//
// end is where the record was read to, for the Stop to record once judged so the
// next Stop in a still-open cycle can mark what this one saw (seen.go). "" when
// nothing was read.
func tagEvents(cmd *cobra.Command, store sessionstate.Store, p HookPayload, reg *module.Registry, bound []string) (evs []event.Event, end string) {
	if replayInjected != nil { // a rule test: the tags as the case gave them (replay_seam.go)
		return injectedOfKinds(replayInjected.tags, bound), ""
	}
	m, ok := reg.Lookup(tagmod.KindPostTagWrite)
	if !ok {
		// No module owns the kind — an impossible state in this build, since
		// tagmod is registered, but handled rather than assumed.
		return nil, ""
	}
	if !boundTo(bound, tagmod.KindPostTagWrite) {
		// Nothing binds to it, so the scan is work done to be discarded — and its
		// cost is a whole-transcript read. Skip it, transcript included.
		return nil, ""
	}

	seen, fresh, end := cycleAgentMessages(cmd, store, p)
	in := module.Input{
		module.InputPhase:        module.PhasePost,
		module.InputSeenMessages: seen,
		module.InputMessages:     fresh,
	}
	evs, err := m.Extract(in)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: module %q: %v\n", m.Name(), err)
	}
	return evs, end
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
