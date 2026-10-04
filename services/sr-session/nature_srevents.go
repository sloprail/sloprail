package main

import (
	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/srevents"
)

// emitGate logs a gate's decision on one event to $SR_EVENTS_FILE. A runner error is a
// refusal here, as it is where the verdict is acted on.
func emitGate(g declaration.Gate, e event.Event, scope hookScope, v dispatchcore.Verdict, err error) {
	ev := srevents.Event{
		Kind: srevents.GateChecked, Rule: srevents.Rule(g.Origin.Plugin, g.Name),
		Outcome: srevents.Permitted, On: e.Kind, ToolUseID: scope.ToolUseID,
	}
	if err != nil {
		ev.Outcome, ev.Reason = srevents.Refused, err.Error()
	} else if v.Refused {
		ev.Outcome, ev.Reason = srevents.Refused, v.Reason
	}
	srevents.Emit(ev)
}

// emitStructure logs a structure gate's decision on one write.
func emitStructure(allowed bool, reason, on, toolUseID string) {
	ev := srevents.Event{Kind: srevents.StructureChecked, Rule: "structure", Outcome: srevents.Permitted, On: on, ToolUseID: toolUseID}
	if !allowed {
		ev.Outcome, ev.Reason = srevents.Refused, reason
	}
	srevents.Emit(ev)
}
