package main

import "github.com/sloprail/sloprail/internal/event"

// replayInjected is the one seam `sr-session replay` uses: the three places the
// engine turns what a HARNESS reported into normalized events — extractPreEvents
// (a tool call about to run), postEvents (what the cycle changed) and tagEvents
// (what the agent wrote) — answer from the events a rule test handed over instead.
//
// Everything after those three points is the real hook: grounding, the context
// enters, the structure gates, the gates, commit-required, the verification of the
// session's tracked ranges, the context exits, the registry of sub-agents. A rule is
// therefore proved against the engine's own dispatch, once, in the engine's own event
// vocabulary — whichever harness the project runs on.
//
// It is nil in every process but a `replay`, which sets it only inside a rule-test
// sandbox (see replay.go).
var replayInjected *replayEvents

// replayEvents are the normalized events a replayed hook acts on.
type replayEvents struct {
	pre  []event.Event // the pending call's events
	post []event.Event // the cycle's Post file events
	tags []event.Event // the cycle's PostTagWrite events
}

// injectedOfKinds keeps the injected events whose kind something binds to, as the
// modules the registry runs only for the bound kinds would have produced.
func injectedOfKinds(evs []event.Event, bound []string) []event.Event {
	var out []event.Event
	for _, e := range evs {
		if boundTo(bound, e.Kind) {
			out = append(out, e)
		}
	}
	return out
}
