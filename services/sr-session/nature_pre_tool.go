package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// This file is the pre-tool half of the new nature dispatch: gates on pre-action
// events, and the structure gate on file-write paths. It is called from
// runSessionPreTool AFTER the old-format dispatch, so both run and a refusal from
// either blocks the tool call.
//
// The return convention matches the old path: it returns an error to end the hook
// command (a deny already written to stdout), or nil to permit. A caller that gets
// a non-nil error stops — the deny has been emitted — exactly as the old dispatch's
// `return deny(...)` does.

// natureVerdict is the outcome of a new-format pre-tool dispatch: the first
// blocking refusal to deny on, if any.
type natureVerdict struct {
	// Blocked is the reason to deny, or "" to permit.
	Blocked string
}

// dispatchNaturePreTool runs the new-format gate and structure-gate dispatch for a
// pre-tool hook, and reports the first refusal to block on (or "" to permit).
//
// It extracts the pre-action events itself from the payload — the same modules the
// old path drives, so a Write produces a PreFileCreate and a Bash a
// PreCommandInvoke — because the new dispatch decides what to enforce from those
// events independently of the old one. Sharing the old path's already-extracted
// events would couple the two dispatches; extracting again is cheap next to a
// model call and keeps the new dispatch's inputs its own.
//
// The FIRST refusal (structure gate checked before gates, then gates in name
// order) is what blocks — a pre-tool hook can only deny once, and denying on the
// first refusal is the same shape the old path takes.
func dispatchNaturePreTool(cmd *cobra.Command, p HookPayload, reg *module.Registry, scope hookScope, store sessionstate.Store) natureVerdict {
	loaded := newNatureDeclarations(cmd, p.Cwd, reg)
	if len(loaded.Gates) == 0 && loaded.Structure == nil {
		// Nothing new-format to enforce at pre-tool. (File-guards and contexts
		// are loaded but not dispatched by this slice, and neither blocks a
		// pre-tool call anyway — a file-guard's own check is the next slice's,
		// and a context does not block.)
		return natureVerdict{}
	}

	events := extractPreEvents(cmd, p, reg, natureBoundKinds(loaded))

	// The structure gate first: a write outside the allowlist is refused before any
	// gate is consulted, because it is the cheapest and most basic "may you write
	// here at all" question, and a path the tree forbids should not also pay for a
	// gate's checks. A refusal here blocks immediately.
	if loaded.Structure != nil {
		if reason := checkStructureGate(cmd, loaded.Structure, events); reason != "" {
			return natureVerdict{Blocked: reason}
		}
	}

	// Then the gates bound to these pre-events. The first that refuses blocks.
	results := runGatesForEvents(cmd, reg, loaded.Gates, events, scope, store)
	for _, r := range results {
		if r.Refused {
			return natureVerdict{Blocked: fmt.Sprintf("%s (gate %s)", r.Reason, r.Name)}
		}
	}
	return natureVerdict{}
}

// checkStructureGate refuses the first file-write event whose target path the
// structure gate does not allow, and returns the refusal reason (or "").
//
// The structure gate is deny-by-default over the tree, so it is checked against
// every path a file-write pre-event names — a create or an update (a delete does
// not write NEW content under a path, so it is not gated by an allowlist of where
// writes may go; the spec frames the structure gate as "is writing HERE allowed").
// The compiled gate is built once per dispatch; a compile failure (unreachable for
// a loaded structure gate) is reported and treated as permitting, since a gate the
// engine could not compile has not established that any path is forbidden — the
// same "an unloadable rule blocks nothing" the rest of the dispatch keeps.
func checkStructureGate(cmd *cobra.Command, sg *declaration.StructureGate, events []event.Event) string {
	compiled, err := dispatchcore.CompileStructureGate(*sg)
	if err != nil {
		// A structure gate that loaded but will not compile is a disagreement
		// between the loader and the runtime. Reported, not enforced: refusing on a
		// gate the engine could not build would blame the author for the engine's
		// gap, and this rule blocks nothing until it compiles.
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: structure gate could not be compiled, so it is NOT enforced: %v\n", err)
		return ""
	}
	for _, e := range events {
		path, ok := writePath(e)
		if !ok {
			continue
		}
		if allowed, reason := compiled.Allows(path); !allowed {
			return reason
		}
	}
	return ""
}

// writePath returns the target path of a file-write pre-event, and whether the
// event is one.
//
// Only PreFileCreate and PreFileUpdate are file WRITES to a path the structure
// gate governs. A PreFileDelete removes a path rather than writing content under
// one, so it is not subject to the write-allowlist — the structure gate answers
// "may a write go here", and a delete is not a write. The path is read off the
// event's `path` field, the flat field filemod declares.
func writePath(e event.Event) (string, bool) {
	switch e.Kind {
	case declaration.KindPreFileCreate, declaration.KindPreFileUpdate:
		if p, ok := e.Fields["path"].(string); ok && p != "" {
			return p, true
		}
	}
	return "", false
}

// extractPreEvents produces the pre-action events for the new dispatch from the
// hook payload, using the same modules the old path uses.
//
// Only the modules something new-format actually binds to are asked — the same
// economy the old path keeps (reg.Needed), for the same reason: producing an event
// nobody bound to is work done to be discarded, and finding the programs a command
// runs is not free. The bound kinds come from the loaded gates and structure gate.
//
// A module returning events ALONGSIDE an error has its events kept and the error
// reported, the module contract the old path also honours — one path a module
// could not classify must not drop the events it did produce.
func extractPreEvents(cmd *cobra.Command, p HookPayload, reg *module.Registry, bound []string) []event.Event {
	in := module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: p,
	}
	var events []event.Event
	for _, m := range reg.Needed(bound) {
		evs, err := m.Extract(in)
		events = append(events, evs...)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: module %q: %v\n", m.Name(), err)
		}
	}
	return events
}

// natureBoundKinds is every event kind the new-format declarations at a pre-tool
// hook actually ask about — the union of each gate's `on` event kinds (with the
// PreFileWrite alias expanded to its concrete pair) plus the structure gate's two
// write kinds.
//
// A gate's `on` carries the event string the author wrote — the loader validates
// but does not rewrite it — so a `PreFileWrite` trigger is expanded here through
// declaration.ExpandGateEvent to the PreFileCreate/PreFileUpdate the modules
// actually emit; otherwise reg.Needed would be asked for a kind no module produces
// and no events would be extracted. Stop is included when a gate names it,
// harmlessly — the pre-tool extraction produces no Stop, so a Stop-only gate simply
// contributes a kind no module here emits. The structure gate binds the two
// file-write kinds so its paths are extracted even when no gate names them.
func natureBoundKinds(loaded declaration.Loaded) []string {
	var bound []string
	for _, g := range loaded.Gates {
		for _, trig := range g.On {
			kinds, _ := declaration.ExpandGateEvent(trig.Event)
			bound = append(bound, kinds...)
		}
	}
	if loaded.Structure != nil {
		bound = append(bound, declaration.KindPreFileCreate, declaration.KindPreFileUpdate)
	}
	return bound
}
