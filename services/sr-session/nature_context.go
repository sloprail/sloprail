package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/natures"
	"github.com/sloprail/sloprail/internal/sessionstate"
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
// (re-)activate on this trigger and leaves the prior state as it was.
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
// preventive file-guard's match reads is populated before that guard runs.
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
const contextsGuardrailKey = "!sloprail:contexts"

// contextStatePrefix keys each context's state under the reserved keyspace, so
// ListState with this prefix reads the whole map back.
const contextStatePrefix = "context:"

// loadContextMap reads the context[] state map from the session store, seeded so
// EVERY declared context has an entry (inactive by default).
//
// This REPLACES the nature_dispatch.go slice's stub, which returned an empty map
// because the context lifecycle did not yet write one. Now it reads the persisted
// contexts and — critically — seeds an inactive `{active:false, payload:{}}` for
// every declared context that has no stored state yet. The seeding is what makes
// `not context["x"].active` a usable match: a declared-but-never-entered context
// must read as present-and-inactive, not as absent, because an absent key makes
// the expression error (fetching `.active` from nil). Every reader — a gate's
// require, a file-guard's match, a context's own enter — sees the same complete
// map.
//
// A store that cannot be read yields the seeded-inactive map (contexts default
// off) rather than failing the dispatch: a rule reading a context before any
// entered sees it inactive, which is the truthful reading, and losing the store
// costs the cross-cycle memory, not the gating.
func loadContextMap(cmd *cobra.Command, store sessionstate.Store, contexts []declaration.Context) map[string]natures.ContextState {
	out := map[string]natures.ContextState{}
	// Seed every declared context inactive first, so an unstored one is present
	// (and false) rather than absent (and an error to index).
	for _, c := range contexts {
		out[c.Name] = natures.ContextState{Active: false, Payload: map[string]any{}}
	}
	if store == nil {
		return out
	}
	entries, err := store.ListState(contextsGuardrailKey, contextStatePrefix)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: context state unavailable, treating all contexts as inactive: %v\n", err)
		return out
	}
	for _, e := range entries {
		name := e.Key[len(contextStatePrefix):]
		var st natures.ContextState
		if json.Unmarshal([]byte(e.Value), &st) != nil {
			continue
		}
		// A stored payload of null unmarshals to a nil map; keep it a non-nil map so
		// a reader indexing `.payload.<key>` gets a clean miss, not an error.
		if st.Payload == nil {
			st.Payload = map[string]any{}
		}
		out[name] = st
	}
	return out
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
) {
	runner := dispatchcore.Runner{}

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
				v, err := runner.Run(dispatchcore.Request{
					Nature:         dispatchcore.NatureFileGuard,
					Require:        c.Require,
					Event:          fired,
					TranscriptPath: scope.Transcript,
					Context:        contextMap,
					Gates:          gatesMap,
					Dir:            c.Dir,
					GuardName:      c.Name,
					Workspace:      scope.Workspace,
					SessionID:      scope.SessionID,
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
				// enter produced unreadable output (fail-closed): report and leave the
				// context's state as it was. Never blocks — a context does not block.
				fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: context %q: %s\n", c.Name, v.Reason)
				continue
			}
			if !active {
				// enter declined to (re-)activate on this trigger: leave the state as
				// it was (an already-active context stays active with its payload).
				continue
			}
			recordContextState(cmd, store, contextMap, c.Name, natures.ContextState{Active: true, Payload: payload})
		}
	}
}

// runContextExits runs every ACTIVE context's `exit` at Stop, flipping the ones
// that say done to inactive — WITHOUT blocking the Stop.
//
// exit is pure lifecycle (the reversal): its verdict only flips `active`. A
// context that says done (non-zero exit, or an exit that could not run — the
// more-guarding direction) is marked inactive, keeping its last payload (spec
// ContextState: the payload survives after the context goes inactive). A context
// that stays active is left untouched. Nothing here contributes to a turn block;
// a Stop gate reading gates[] does that, and it has already run by the time this
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
) {
	runner := dispatchcore.Runner{}

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

		done, _, err := runner.ExitContext(dispatchcore.ContextExitRequest{
			Exit:           c.Exit,
			Event:          stop,
			TranscriptPath: scope.Transcript,
			Gates:          gatesMap,
			CurrentContext: current,
			Dir:            c.Dir,
			Name:           c.Name,
			Workspace:      scope.Workspace,
			SessionID:      scope.SessionID,
			// Re-entry provenance for an exit that spawns sr-agent: this context
			// appended to the stack, so its own launched agent is not re-entered by
			// it (isLaunchedBy above, one exec down). See the gate dispatch.
			LaunchedBy: appendLaunchedBy(os.Getenv, c.Name),
		})
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: context %q exit: %v\n", c.Name, err)
			continue
		}
		if !done {
			// NOT done (a non-zero "stay active" exit, or an exit that could not run —
			// both stay active, the more-guarding direction). Leave the context as it
			// is for another cycle. The reason a could-not-run carries is deliberately
			// NOT logged as an error here: a non-zero exit is the ORDINARY way a
			// context says "not done yet", so treating every one as a fault would make
			// the common path noisy. A genuinely broken exit shows up as a context
			// that never deactivates, which is visible on its own.
			continue
		}
		// Done (clean exit): mark inactive, KEEPING the last payload so a later cycle
		// can still read what the closed context last measured.
		recordContextState(cmd, store, contextMap, c.Name, natures.ContextState{Active: false, Payload: current.Payload})
	}
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
	out := make(map[string]any, len(contextMap))
	for name, st := range contextMap {
		payload := st.Payload
		if payload == nil {
			payload = map[string]any{}
		}
		out[name] = map[string]any{
			"active":  st.Active,
			"payload": payload,
		}
	}
	return out
}
