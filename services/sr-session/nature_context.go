package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/checkrun"
	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/natures"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/srevents"
)

// This file is the CONTEXT-LIFECYCLE half of the new nature dispatch (3b): a
// context is an activatable scope with an enter/exit lifecycle
// (dot-dir-file-store/main.tsp ContextDeclaration). Where a gate blocks and a
// file-guard re-fires, a context TRACKS — it contributes an entry in the
// context[] map that guards, gates (via `require: [{context}]`), and other
// contexts read.
//
// # enter, on every occurrence
//
// enter runs on EVERY `on` trigger whose kind fired and whose `match` holds,
// active or not (the reversal of the enter/watch split, spec 2026-08-19). It
// receives ContextEnterPayload (its own currentContext included), and its stdout
// REPLACES the context's payload — a script growing a list reads
// currentContext.payload and returns the grown version. A clean enter sets the
// context ACTIVE and stores the (replaced) payload; a non-zero enter declines to
// (re-)activate on this trigger and leaves the prior state as it was. An enter that
// CANNOT RUN (missing, not executable, no shebang, killed) is not a decline: it
// refuses the event that triggered it (a Pre* kind is denied, a Post* kind refuses the
// Stop that handled it), because a context left off silently disarms every rule that
// reads it. runContextEnters returns those refusals to its callers.
//
// # exit, at Stop, PURE lifecycle
//
// exit runs on Stop while the context is active (it has no `on` of its own). It
// does NOT block the Stop — the reversal (spec slice 7): exit only flips this
// context's own `active`. A gate bound to Stop is what blocks a turn, reading
// gates[]. So a context's exit verdict here sets active=false (done) and NEVER
// contributes a refusal to the turn.
//
// # The ordering at Stop (load-bearing)
//
//	context enters (on the cycle's Post events)   — populate context[]
//	    → gates at Stop (read context[])           — a gate may require a context
//	        → context exits (after gates decided)  — the context then closes
//
// A gate that requires a context must see it ACTIVE, so enters run first; a
// context's exit runs after the gates so the gate reads the context still open,
// then the context closes for the next cycle. runNatureStopCycle orchestrates
// this; the enters also run at PRE-tool (on the pre events) so a context a
// gate's match reads is populated before that gate runs.
//
// # Persistence
//
// The context[] map is kept in the session store under the reserved keyspace
// contextsGuardrailKey (`!sloprail:contexts`), symmetric to gates[]
// (`!sloprail:gates`): per-guardrail state under a name no real rule can have (a
// context's folder name cannot contain '!' or ':'), one `context:<name>` entry
// each, JSON-encoded ContextState. Loaded once per dispatch, updated per
// enter/exit, saved back — so a later cycle, a gate, and a file-guard read what
// the last enter left, and a context's LAST payload survives after it goes
// inactive (spec ContextState).

// contextsGuardrailKey is the reserved sessionstate keyspace the context[] map is
// persisted under — symmetric to gatesGuardrailKey.
const contextsGuardrailKey = checkrun.ContextsGuardrailKey

// contextStatePrefix keys each context's state under the reserved keyspace, so
// ListState with this prefix reads the whole map back.
const contextStatePrefix = checkrun.ContextStatePrefix

// loadContextMap reads the context[] state map from the session store, seeded so EVERY
// declared context has an entry (inactive by default): see checkrun.LoadContextMap.
func loadContextMap(cmd *cobra.Command, store sessionstate.Store, contexts []declaration.Context) map[string]natures.ContextState {
	return checkrun.LoadContextMap(cmd.ErrOrStderr(), store, contexts)
}

// recordContextState writes one context's state to the store and updates the
// in-memory map so later contexts in the same dispatch see it.
//
// Persisted under the reserved keyspace so the next cycle, a gate, and a
// file-guard read what enter/exit left. A write failure is reported and swallowed
// — the engine's own bookkeeping going wrong is not the project's rule being
// violated, and refusing the agent's work over it is a refusal no context asked
// for (the same stance recordGateVerdict takes).
func recordContextState(cmd *cobra.Command, store sessionstate.Store, contextMap map[string]natures.ContextState, name string, st natures.ContextState) {
	if st.Payload == nil {
		st.Payload = map[string]any{}
	}
	contextMap[name] = st
	if store == nil {
		return
	}
	value, err := json.Marshal(st)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: context state not recorded: %v\n", err)
		return
	}
	if err := store.SetState(contextsGuardrailKey, contextStatePrefix+name, string(value)); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: context state not recorded: %v\n", err)
	}
}

// runContextEnters runs every context's `enter` for each fired event its `on`
// triggers match, updating the context[] map in place (and the store).
//
// enter fires on EVERY matching occurrence (spec), so unlike a gate this does not
// stop at the first trigger — a context may enter on several events in one cycle,
// each re-running enter with the latest state. For each (context, fired event):
// its `require` is checked first (through the Runner, a pure-require run) — a
// missing prerequisite skips this occurrence's enter (the context does not enter
// on a trigger whose preconditions are unmet). Then enter runs; a clean run sets
// active and replaces the payload, a decline leaves the state as it was.
//
// The gates[] map is threaded so enter reads `gates[<name>].status`. The context
// map is mutated in place so a context entering earlier in the loop is visible to
// one whose `require` names it later (the ordering `{context}` needs, within this
// cycle's enters).
func runContextEnters(
	cmd *cobra.Command,
	reg *module.Registry,
	contexts []declaration.Context,
	events []event.Event,
	scope hookScope,
	store sessionstate.Store,
	contextMap map[string]natures.ContextState,
	gatesMap map[string]natures.GateState,
	histories map[string]*dispatchcore.FileHistory,
) []contextRefusal {
	runner := dispatchcore.Runner{}
	var refused []contextRefusal

	for _, c := range contexts {
		if isLaunchedBy(os.Getenv, c.Name) {
			// This session is a launched check of this very context; do not re-enter
			// it against its own agent's work.
			continue
		}
		for _, fired := range contextMatchingEvents(cmd, reg, c, events, contextMap) {
			// require first — a context whose precondition is unmet does not enter on
			// this occurrence. Evaluated through the Runner (a pure-require run reads
			// the same context/transcript the checks would), so `{skill}` and
			// `{context}` behave exactly as they do for a gate.
			if len(c.Require) > 0 {
				path, _ := fired.Fields[filemod.FieldPath].(string)
				v, err := runner.Run(dispatchcore.Request{
					Nature:  dispatchcore.NatureFileGuard,
					Require: c.Require,
					Event:   fired,
					// A Post file event's history, as a file-guard's gets it: a
					// citation grounds only the change it rode on, for a
					// context's requirement as for any other.
					History:        histories[path],
					TranscriptPath: scope.Transcript,
					Context:        contextMap,
					Gates:          gatesMap,
					Dir:            c.Dir,
					GuardName:      c.Name,
					Workspace:      scope.Workspace,
					SessionID:      scope.SessionID,
					AgentID:        scope.AgentID,
					// Re-entry provenance, in case a `{skill}`-style require ever runs a
					// check that spawns sr-agent: this context appended to the stack, so
					// its own launched agent is not re-entered by it (isLaunchedBy above).
					LaunchedBy: appendLaunchedBy(os.Getenv, c.Name),
				})
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: context %q require: %v\n", c.Name, err)
					continue
				}
				if v.Refused {
					// Precondition unmet: skip enter on this occurrence. Not an error
					// and not a refusal of anything — a context simply does not enter
					// on a trigger it is not ready for.
					continue
				}
			}

			current := contextMap[c.Name]
			payload, active, v, err := runner.EnterContext(dispatchcore.ContextEnterRequest{
				Enter:          c.Enter,
				Event:          fired,
				TranscriptPath: scope.Transcript,
				Gates:          gatesMap,
				CurrentContext: current,
				Dir:            c.Dir,
				Name:           c.Name,
				Workspace:      scope.Workspace,
				SessionID:      scope.SessionID,
				AgentID:        scope.AgentID,
				// Re-entry provenance for an enter that spawns sr-agent: this context
				// appended to the stack, so its own launched agent is not re-entered
				// by it (isLaunchedBy above, one exec down). See the gate dispatch.
				LaunchedBy: appendLaunchedBy(os.Getenv, c.Name),
			})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: context %q enter: %v\n", c.Name, err)
				continue
			}
			if v.Refused {
				// enter could not run, or printed output that is no flat JSON object. That is
				// not a decline: the context stays as it was (not entered), and a rule that
				// reads it would silently not fire. So the trigger is REFUSED (the caller
				// denies a Pre* event, and blocks the Stop that handled a Post* one), and the
				// reason is still reported on stderr.
				fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: context %q: %s\n", c.Name, v.Reason)
				if repairsContext(events, c, scope.Workspace) {
					// The call repairs the context's own script: refusing it would make the fault
					// unfixable. The Stop sweep still refuses while the fault stands.
					continue
				}
				refused = append(refused, contextRefusal{Context: c.Name, Reason: v.Reason})
				break // one refusal per context per dispatch: the next occurrence would say the same
			}
			if !active {
				// enter declined to (re-)activate on this trigger: leave the state as
				// it was (an already-active context stays active with its payload).
				continue
			}
			if !current.Active {
				srevents.Emit(srevents.Event{Kind: srevents.ContextActivated, Rule: srevents.Rule(c.Origin.Plugin, c.Name), On: fired.Kind, ToolUseID: scope.ToolUseID})
			}
			recordContextState(cmd, store, contextMap, c.Name, natures.ContextState{Active: true, Payload: payload})
		}
	}
	return refused
}

// contextRefusal is a context whose enter could not be run, with the reason to give the agent.
type contextRefusal struct {
	Context string
	Reason  string
}

// contextRefusalReasons renders the refusals as the texts a deny or a Stop block carries.
func contextRefusalReasons(rs []contextRefusal) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Reason+" (context "+r.Context+")")
	}
	return out
}

// brokenContextScripts is the Stop-time sweep over every declared context's enter and exit:
// one whose script cannot run is refused, so the turn cannot end with the mode silently off,
// even for a context that was never triggered (nothing else would have said so). It reads the
// files, not the history, so it stops refusing the moment the script is fixed. A context whose
// enter already refused at this Stop (seen) is not named twice.
func brokenContextScripts(contexts []declaration.Context, seen []contextRefusal) []string {
	done := map[string]bool{}
	for _, r := range seen {
		done[r.Context] = true
	}
	var out []string
	for _, c := range contexts {
		for _, s := range []struct{ role, script string }{{"enter", c.Enter}, {"exit", c.Exit}} {
			if s.role == "enter" && done[c.Name] {
				continue
			}
			if err := dispatchcore.ContextScriptFault(c.Dir, s.script); err != nil {
				consequence := "it is never entered, so what it guards cannot be judged"
				if s.role == "exit" {
					consequence = "it can never be closed, so what it guards stays in force unjudged"
				}
				out = append(out, fmt.Sprintf(
					"the %q context's %s script %q cannot run (%v); %s. This turn is refused until the script is fixed "+
						"(it must exist, be executable (`chmod +x`) and start with `#!/usr/bin/env bash`) (context %s)",
					c.Name, s.role, s.script, err, consequence, c.Name))
			}
		}
	}
	return out
}

// runContextExits runs every ACTIVE context's `exit` at Stop, flipping the ones
// that say done (a clean exit) to inactive. A plain "not done" never blocks the Stop; an exit
// that could not finish (killed on its timeout) stays active AND is returned as a refusal.
//
// exit is lifecycle (the reversal): its verdict flips `active`. A
// context that says done is marked inactive, keeping its last payload (spec
// ContextState: the payload survives after the context goes inactive). A context
// that stays active is left untouched. A Stop gate reading gates[] does the usual
// blocking, and it has already run by the time this
// is called (see runNatureStopCycle).
//
// Runs AFTER the Stop gates so a gate requiring a context reads it still active,
// then the context closes for the next cycle.
func runContextExits(
	cmd *cobra.Command,
	contexts []declaration.Context,
	stop event.Event,
	scope hookScope,
	store sessionstate.Store,
	contextMap map[string]natures.ContextState,
	gatesMap map[string]natures.GateState,
) []string {
	runner := dispatchcore.Runner{}
	var refusals []string

	for _, c := range contexts {
		current := contextMap[c.Name]
		if !current.Active {
			// exit is consulted only on an ACTIVE context (spec). An inactive one has
			// nothing to close.
			continue
		}
		if isLaunchedBy(os.Getenv, c.Name) {
			continue
		}
		if dispatchcore.ContextScriptFault(c.Dir, c.Exit) != nil {
			// A script that cannot be run at all is already named by brokenContextScripts.
			continue
		}

		done, fault, err := runner.ExitContext(dispatchcore.ContextExitRequest{
			Exit:           c.Exit,
			Event:          stop,
			TranscriptPath: scope.Transcript,
			Gates:          gatesMap,
			CurrentContext: current,
			Dir:            c.Dir,
			Name:           c.Name,
			Workspace:      scope.Workspace,
			SessionID:      scope.SessionID,
			AgentID:        scope.AgentID,
			// Re-entry provenance for an exit that spawns sr-agent: this context
			// appended to the stack, so its own launched agent is not re-entered by
			// it (isLaunchedBy above, one exec down). See the gate dispatch.
			LaunchedBy: appendLaunchedBy(os.Getenv, c.Name),
		})
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: context %q exit: %v\n", c.Name, err)
			continue
		}
		if fault != "" {
			// An exit that could not run (killed on its timeout, or the file went bad since the
			// sweep) never answered: the context stays active and the turn is refused, never
			// passed with the mode silently left open.
			refusals = append(refusals, fmt.Sprintf(
				"the %q context's exit script %q could not finish (%s); the context stays active, so what it guards stays in force unjudged. "+
					"This turn is refused until the exit answers (context %s)", c.Name, c.Exit, fault, c.Name))
			continue
		}
		if !done {
			// A non-zero exit is the ORDINARY way a context says "not done yet": it stays
			// active for another cycle, quietly.
			continue
		}
		// Done (clean exit): mark inactive, KEEPING the last payload so a later cycle
		// can still read what the closed context last measured.
		srevents.Emit(srevents.Event{Kind: srevents.ContextDeactivated, Rule: srevents.Rule(c.Origin.Plugin, c.Name), On: stop.Kind})
		recordContextState(cmd, store, contextMap, c.Name, natures.ContextState{Active: false, Payload: current.Payload})
	}
	return refusals
}

// contextMatchingEvents returns every fired event a context's `on` triggers
// match — ALL of them, because enter fires on every matching occurrence, not just
// the first (unlike a gate, which is one-shot).
//
// A context wakes on an event when one of its triggers names a kind that fired
// AND that trigger's `match` (compiled against the context scope for the kind)
// holds. The same (trigger, event) pair may appear once; duplicate events for the
// same kind each fire enter, which is the "every occurrence" the spec names.
//
// # Why a match error here does NOT refuse (and how it still fails safe)
//
// A file-guard and a gate fail CLOSED on a match the engine cannot evaluate: they
// have an action to refuse (a write, a tool call), so a match they cannot decide
// becomes a refusal rather than being read as approval (matcher.go:121/186). A
// CONTEXT has no such channel. It takes no action at match time — it only decides
// whether to ACTIVATE a scope — so there is nothing to "refuse" here, and turning a
// context match error into a turn block would invent a refusal no context declared
// (a context's own exit is defined never to block the turn; its match cannot be
// louder than its lifecycle).
//
// The fail-safe direction for a context is therefore "the scope does not open": a
// trigger whose match cannot be evaluated does NOT count as a match, so the context
// does not enter on that occurrence. This is the conservative reading in the sense
// that matters for a context — a context match error cannot be assumed TRUE, so the
// engine must not activate a scope it could not confirm the trigger for. (The
// opposite risk — a PROTECTIVE context that fails to open because its own trigger
// erred — is real, but it is not fixable here: staying out of a protective scope is
// unsafe, yet a match the engine cannot answer cannot be assumed true either, and a
// context has no third "refuse" answer a file-guard/gate can fall back to. A rule
// that must HARD-fail on such an error belongs in a gate, which has the channel.)
//
// So the error is REPORTED loudly (stderr, every dispatch) and the trigger treated
// as non-matching — deliberately, for the reason above, NOT as the silent
// no-op the file-guard/gate paths were wrongly doing before this fix. A context
// whose match keeps erring shows up as a scope that never opens, which is visible
// on its own; none of the suites here exercise a context match-eval error, and this
// is documented rather than left as a bare `continue`.
func contextMatchingEvents(cmd *cobra.Command, reg *module.Registry, c declaration.Context, events []event.Event, contextMap map[string]natures.ContextState) []event.Event {
	var matched []event.Event
	seen := map[int]bool{}
	for _, trig := range c.On {
		kinds, _ := declaration.ExpandContextEvent(trig.Event)
		for i, e := range events {
			if seen[i] {
				continue
			}
			if !containsKind(kinds, e.Kind) {
				continue
			}
			kindDecl, known := reg.KindDeclFor(e.Kind)
			if !known {
				continue
			}
			m, err := guardrail.CompileContextMatch(trig.Match, kindDecl)
			if err != nil {
				// Compile disagreeing with load (unreachable for a loaded context). A
				// context has no refusal channel (see the doc comment above), so the
				// fail-safe is "the scope does not open": report loudly and do not enter
				// on this occurrence.
				fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: context %q trigger on %s could not be compiled, so the context does not enter on it: %v\n", c.Name, trig.Event, err)
				continue
			}
			ok, err := m.Match(contextMatchEvent(e, contextMap))
			if err != nil {
				// The match compiled but could not be evaluated. As above, a context
				// has no action to refuse, so the fail-safe direction is that the scope
				// does not open: report loudly and treat as non-matching (NOT the silent
				// no-op the file-guard/gate paths were wrongly doing before this fix).
				fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: context %q trigger on %s could not be evaluated, so the context does not enter on it: %v\n", c.Name, trig.Event, err)
				continue
			}
			if ok {
				matched = append(matched, e)
				seen[i] = true
			}
		}
	}
	return matched
}

// contextMatchEvent wraps a fired event in the NESTED shape a context trigger's
// match reads: the event's fields under `event`, and the context map (wire form)
// under `context` — the ContextMatchScope shape, identical to a gate's
// GateMatchScope.
//
// The context map is passed so a trigger's `match` MAY read `context[<name>]`
// (the scope declares it). It is threaded in wire form (contextMatchValue) so
// `context["x"].active` indexes correctly; nil is fine for a trigger that reads
// no context, which is the common case.
func contextMatchEvent(e event.Event, contextMap map[string]natures.ContextState) event.Event {
	return event.Event{Kind: e.Kind, Fields: map[string]any{
		"event":   e.Fields,
		"context": contextMatchValue(contextMap),
	}}
}

// contextMatchValue converts a typed context[] map into the wire form an
// expression indexes — `map[string]any{name: {"active":…, "payload":…}}`.
//
// This is REQUIRED for a matcher: expr reads a struct's fields by their Go
// exported names and does NOT honour json tags, so a natures.ContextState struct
// in the env fails `context[<name>].active` ("cannot fetch active from
// ContextState"). The wire form — lowercase keys the spec's expressions name — is
// what makes `context["x"].active`, `not context["x"].active`, and
// `context["x"].payload.<key>` all evaluate. Every declared context is already
// present in the map (loadContextMap seeds inactive ones), so an inactive context
// reads as `{active:false, payload:{}}` rather than an absent key that would error.
//
// (A check/judge payload does NOT need this — it is JSON-serialized, and json
// tags produce the same lowercase keys there. This conversion is for the matcher
// env only, which reads Go values through reflection.)
func contextMatchValue(contextMap map[string]natures.ContextState) map[string]any {
	return checkrun.ContextMatchValue(contextMap)
}
