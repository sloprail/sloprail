package main

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/cyclemod"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// This file is the Stop half of the new nature dispatch: gates bound to Stop, run
// at the end of a cycle. It is called from completeCycle AFTER the old-format Post
// dispatch, so both run and a refusal from either blocks the turn.
//
// A Stop gate is where the "don't stop until X" rules live — a gate whose check
// confirms every citation resolves, or whose require names a context that must
// have activated. It blocks the turn on a refusal, which sends the agent round
// again over the same cycle, the only mechanism an end-of-cycle rule has to get
// anything corrected (a Post event cannot undo the work; it can only refuse to let
// the turn end).

// dispatchNatureStop runs the new-format gates bound to Stop and reports the text
// to block the turn with (or "" to let it end).
//
// The event is the single Stop cyclemod produces — subjectless, unconditional
// (the cycle ended whatever the tree looks like). Every gate whose `on` names Stop
// and whose match holds is run through the shared runGatesForEvents, so a Stop gate
// records its verdict into the gates[] map the same way a pre-event gate does.
//
// Every refusal is collected and reported together, not just the first: the agent
// is about to spend a turn on this, and being handed one violation at a time turns
// one correction into as many turns as there are gates — the same reason the old
// Post dispatch collects all its objections. Each names its gate.
func dispatchNatureStop(cmd *cobra.Command, p HookPayload, reg *module.Registry, scope hookScope, store sessionstate.Store) string {
	loaded := newNatureDeclarations(cmd, p.Cwd, reg)
	if len(loaded.Gates) == 0 {
		return ""
	}

	// The Stop event, built by cyclemod so the kind's name and its empty fields are
	// stated where they are declared. Only gates naming Stop will match it; a gate
	// bound to a pre-event contributes nothing here.
	events := []event.Event{cyclemod.Event()}

	results := runGatesForEvents(cmd, reg, loaded.Gates, events, scope, store)
	var refusals []string
	for _, r := range results {
		if r.Refused {
			refusals = append(refusals, refusalLine(r))
		}
	}
	if len(refusals) == 0 {
		return ""
	}
	if len(refusals) == 1 {
		return refusals[0]
	}
	return "the following gates refused this turn's work:\n  - " + strings.Join(refusals, "\n  - ")
}

// refusalLine renders one gate's refusal, naming the gate — a refusal an agent
// cannot attribute to a rule is one it cannot act on.
func refusalLine(r gateResult) string {
	return r.Reason + " (gate " + r.Name + ")"
}
