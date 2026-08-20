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

// This file is the NEW nature-based dispatch, wired ALONGSIDE the old GUARDRAIL.md
// dispatch at the same hook points — both run, and neither disarms the other. The
// old dispatch reads `.sloprail/guardrails/<name>/GUARDRAIL.md`; this reads the
// five new formats under `.sloprail/` (via internal/declaration). Until the old
// format is removed (a later wave), a project may declare rules in either, and a
// session is guarded by both.
//
// This slice implements the two SIMPLEST natures end to end — the gate (3a) and
// the structure gate (3s) — on top of the shared check-runner in internal/dispatch.
// The file-guard and context natures build on the same runner in the next slice;
// their loaders already exist, so this dispatch loads them (a malformed one is
// reported), but does not yet dispatch them.
//
// # Where it hooks in
//
//   - PRE-TOOL (session_pre_tool.go): the pre-action events a cycle is about to
//     perform. Gates whose `on` trigger matches a fired pre-event are run here and
//     BLOCK the tool call on a refusal (deny). The structure gate is checked on
//     every file-write pre-event and blocks a write outside its allowlist.
//   - STOP (session_stop.go): the end of a cycle. Gates bound to Stop are run here
//     and BLOCK the turn on a refusal (block). A gate's verdict is recorded into
//     the gates[] map whichever hook produced it.
//
// # Fail-closed and reporting, mirroring the old dispatch
//
// A declaration that could not be LOADED is reported (never silently dropped) and
// dispatches nothing — the same "an invalid guardrail blocks nothing, but is named
// every time" rule the old format settled on. A check that could not be RUN is a
// refusal (the check-runner's fail-closed default). The blocking channels are the
// harness's: deny() at pre-tool, block() at Stop — the two measured to both stop
// the action and carry their words.

// natureDispatchPreTool resolves the hook scope and session store, runs the
// new-format pre-tool dispatch, and returns the reason to deny (or "").
//
// A thin wrapper over dispatchNaturePreTool that owns the store's lifetime — opened
// here, closed here — so the hook command calls one function and does not manage a
// second store alongside the old path's revalidation store. The store is a
// convenience for the gates[] map (a gate still decides without it); a store that
// will not open is reported and the dispatch runs storeless.
func natureDispatchPreTool(cmd *cobra.Command, p HookPayload, reg *module.Registry) string {
	scope := natureHookScope(cmd, p)
	store := natureStore(cmd, p)
	if store != nil {
		defer store.Close()
	}
	return dispatchNaturePreTool(cmd, p, reg, scope, store).Blocked
}

// natureDispatchStop resolves the scope and store, runs the new-format Stop
// dispatch, and returns the text to block the turn with (or "").
//
// The Stop counterpart of natureDispatchPreTool, owning the store's lifetime the
// same way.
func natureDispatchStop(cmd *cobra.Command, p HookPayload, reg *module.Registry) string {
	scope := natureHookScope(cmd, p)
	store := natureStore(cmd, p)
	if store != nil {
		defer store.Close()
	}
	return dispatchNatureStop(cmd, p, reg, scope, store)
}

// natureHookScope resolves the session identity, workspace and transcript for the
// new dispatch — the same three the old path resolves into a hookScope, derived the
// same way (stableID for the identity, p.record for the transcript), so a gate's
// check reads the same session facts the old hooks do. A session that cannot be
// identified yields an empty SessionID, which is not a reason to refuse: a gate
// deciding on the event's own facts still works, and a `{skill}` prerequisite that
// needs the transcript fails closed on its own if the record is absent.
func natureHookScope(cmd *cobra.Command, p HookPayload) hookScope {
	scope := hookScope{Workspace: p.Cwd}
	if id, err := stableID(p); err == nil {
		scope.SessionID = id
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: %v\n", err)
	}
	if path, err := p.record(); err == nil {
		scope.Transcript = path
	}
	return scope
}

// natureStore opens the session store the gates[] map is kept in, or nil.
//
// The same store the engine's own hook points open (openEngineState), keyed by the
// conversation's identity. A store that cannot be opened is reported and nil is
// returned — the gates map is then in-memory only for this dispatch, which is
// enough to gate on this cycle's events but loses the cross-cycle read a context
// will want next slice. That is the same "bookkeeping unavailable, judge anyway"
// stance the old dispatch takes when its own store will not open.
func natureStore(cmd *cobra.Command, p HookPayload) sessionstate.Store {
	store, err := openEngineState(p)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: gate state unavailable, gate verdicts not persisted this cycle: %v\n", err)
		return nil
	}
	return store
}

// gatesGuardrailKey is the reserved sessionstate keyspace the gates[] map is
// persisted under.
//
// It is stored as ordinary per-guardrail state (State/SetState), but under a name
// no real rule can have: a guardrail's name is its folder name under
// `.sloprail/gate/`, which cannot contain '!' or ':', so this cannot collide with
// any gate's own keyspace. Persisting it as guardrail-state rather than inventing
// a new table keeps the gates map in the store that already dies with the session
// and is already keyed by it — the same lifetime and location a context's own
// state will want when it reads these verdicts next slice.
const gatesGuardrailKey = "!sloprail:gates"

// gateStatePrefix keys each gate's verdict under the reserved keyspace, so the map
// is one entry per gate and ListState with this prefix reads them all back.
const gateStatePrefix = "gate:"

// newNatureDeclarations loads the project's new-format `.sloprail` declarations,
// reporting any that could not be loaded on the same channel the old format uses.
//
// Rooted at the project's own `.sloprail` (dotDir), NOT the plugin-resolving store
// the old format uses: this slice reads one project's own declarations. Plugin
// distribution of new-format rules is out of scope here, the same boundary
// internal/declaration.Store draws.
//
// A registry is required so trigger matches can be evaluated — the loader compiles
// them against the module vocabulary. Returns the loaded set; a load error at the
// store level (the directory could not be listed) is reported and yields an empty
// set, matching the old dispatch's "report and permit" for an unreadable store.
func newNatureDeclarations(cmd *cobra.Command, cwd string, reg *module.Registry) declaration.Loaded {
	store := declaration.New(dotDir(cwd))
	loaded, err := store.Load(reg)
	if err != nil {
		// The store itself could not be read. Reported and treated as empty, the
		// same as the old dispatch does for an unreadable guardrails directory: an
		// engine deciding on its own that nothing was declared is the fail-open this
		// codebase closes elsewhere, but a directory that will not list gives it
		// nothing to enforce and refusing every action punishes a fault the agent
		// cannot fix.
		fmt.Fprintf(cmd.ErrOrStderr(),
			"sloprail: the new-format declarations in this project could not be read: %v\n", err)
		return declaration.Loaded{}
	}
	reportNatureInvalid(cmd, loaded.Invalid)
	return loaded
}

// reportNatureInvalid names every new-format declaration that could not be loaded,
// one line per fault — the same shape reportInvalid uses for the old format.
//
// Reported rather than fatal: a broken declaration blocks nothing (it dispatches
// nothing below), but is named every time so an author fixing it sees all of it.
// The channel is stderr, which reaches a person tailing logs; a refusal is not
// raised for a rule that could not load, matching the old format's settled rule.
func reportNatureInvalid(cmd *cobra.Command, invalid []declaration.Invalid) {
	for _, iv := range invalid {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: declaration %s not loaded:\n", iv.Qualified())
		for _, reason := range iv.Reasons {
			fmt.Fprintf(cmd.ErrOrStderr(), "  - %s\n", reason)
		}
	}
}

// gateResult is one gate's outcome on one fired event: the gate's name, whether it
// refused, and the reason to relay.
type gateResult struct {
	Name    string
	Refused bool
	Reason  string
}

// runGatesForEvents runs every gate whose `on` trigger matches one of the fired
// events, in the context of the session, and returns the results in gate-name
// order.
//
// This is the shared body both hook points call — pre-tool with its pre-events,
// Stop with the Stop event. For each gate, the FIRST of its triggers that matches
// any fired event wakes it (a gate runs once per dispatch even if two triggers
// match; it decides at the moment, one-shot). The check-runner then evaluates
// require + checks and produces the verdict, which is recorded into the gates[]
// map here so a later cycle or a context can read it.
//
// The context[]/gates[] maps read by the runner come from the store (loadGatesMap)
// — for THIS slice the context map is whatever context state exists (empty until
// the context slice populates it), threaded through so that slice changes only the
// map's source, not this dispatch.
func runGatesForEvents(
	cmd *cobra.Command,
	reg *module.Registry,
	gates []declaration.Gate,
	events []event.Event,
	scope hookScope,
	store sessionstate.Store,
) []gateResult {
	if len(gates) == 0 {
		return nil
	}

	// The state maps the runner reads and records into. Loaded once for the whole
	// dispatch so several gates see a consistent world; contexts are read-only here
	// (populated next slice), gates are read and then written back per verdict.
	contextMap := loadContextMap(cmd, store)
	gatesMap := loadGatesMap(cmd, store)

	runner := dispatchcore.Runner{}
	var results []gateResult

	for _, g := range gates {
		fired, ok := firstMatchingEvent(cmd, reg, g, events)
		if !ok {
			continue
		}

		// This session is running underneath THIS gate's own launched agent (a
		// judge check launches sr-agent, whose own writes fire pre-tool). Do not
		// enforce the gate against itself — the same re-entry guard the old
		// dispatch applies per guardrail. Other rules still run.
		if isLaunchedBy(os.Getenv, g.Name) {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"sloprail: gate %q not enforced here — this session was launched by its own check (%s)\n",
				g.Name, LaunchedByEnv)
			continue
		}

		verdict, err := runner.Run(dispatchcore.Request{
			Nature:         dispatchcore.NatureGate,
			Require:        g.Require,
			Checks:         g.Checks,
			Event:          fired,
			TranscriptPath: scope.Transcript,
			Context:        contextMap,
			Gates:          gatesMap,
			Dir:            g.Dir,
			GuardName:      g.Name,
		})
		if err != nil {
			// The runner itself could not decide (a programming error, not a check
			// refusal — the runner turns a check that cannot run into a refusal
			// rather than an error). Fail-closed: refuse, naming the gate.
			fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: gate %q: %v\n", g.Name, err)
			results = append(results, gateResult{
				Name:    g.Name,
				Refused: true,
				Reason:  fmt.Sprintf("the gate %q could not be evaluated (%v); refusing because a gate that could not decide must not be read as approval", g.Name, err),
			})
			recordGateVerdict(cmd, store, gatesMap, g.Name, natures.GateStatusFail)
			continue
		}

		status := natures.GateStatusPass
		if verdict.Refused {
			status = natures.GateStatusFail
		}
		recordGateVerdict(cmd, store, gatesMap, g.Name, status)

		if verdict.Refused {
			results = append(results, gateResult{Name: g.Name, Refused: true, Reason: verdict.Reason})
		}
	}
	return results
}

// firstMatchingEvent returns the first fired event a gate's `on` triggers match,
// and whether any did.
//
// A gate wakes when one of its triggers names an event kind that fired AND that
// trigger's `match` (compiled against the gate scope for the kind) evaluates true.
// The first such (trigger, event) pair is what the gate decides about — a gate is
// one-shot, so it runs once even if several triggers or events would match.
//
// A trigger's match is compiled here rather than at load because the KIND the
// event carries is needed to build the scope — the same reason the old dispatch
// compiles a matcher at the hook point. A compile that fails (unreachable for a
// loaded gate, whose triggers the loader already compiled) or an evaluation error
// is reported and treated as non-matching for that trigger, so a gate does not
// wake on a match it could not actually confirm.
func firstMatchingEvent(cmd *cobra.Command, reg *module.Registry, g declaration.Gate, events []event.Event) (event.Event, bool) {
	for _, trig := range g.On {
		// The trigger's `event` may be the PreFileWrite alias; expand it to the
		// concrete kinds it fires on, the same table the loader validated it
		// against. A trigger that loaded is always known.
		kinds, _ := declaration.ExpandGateEvent(trig.Event)
		for _, e := range events {
			if !containsKind(kinds, e.Kind) {
				continue
			}
			kindDecl, known := reg.KindDeclFor(e.Kind)
			if !known {
				continue
			}
			m, err := guardrail.CompileGateMatch(trig.Match, kindDecl)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: gate %q trigger on %s: %v\n", g.Name, trig.Event, err)
				continue
			}
			ok, err := m.Match(gateMatchEvent(e))
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: gate %q trigger on %s: %v\n", g.Name, trig.Event, err)
				continue
			}
			if ok {
				return e, true
			}
		}
	}
	return event.Event{}, false
}

// containsKind reports whether kind is one of the concrete kinds a trigger
// expanded to.
func containsKind(kinds []string, kind string) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// gateMatchEvent wraps a fired event in the NESTED shape a gate matcher reads: the
// event's own fields under `event`, so `event.path` / `event.invocations` resolve.
//
// This is the runtime env shape CompileGateMatch documents — a gate scope nests
// where a file scope is flat. The context map is deliberately NOT included here:
// for this slice no gate trigger's `match` reads `context` in the examples, and
// threading the context state into the matcher env is a refinement the context
// slice makes when it populates that state. A trigger that does read `context`
// would see it absent (undefined), which its own `require: [{context}]` — always
// present alongside such a match in the examples — then refuses on.
func gateMatchEvent(e event.Event) event.Event {
	return event.Event{Kind: e.Kind, Fields: map[string]any{"event": e.Fields}}
}

// -- the gates[] map persistence --

// loadGatesMap reads every gate's most recent verdict from the session store.
//
// Stored as per-guardrail state under the reserved keyspace, one entry per gate.
// A store that cannot be read yields an empty map rather than failing the
// dispatch: a gate that cannot read prior verdicts still decides on this event's
// own facts, and the map is a convenience for cross-cycle reads (a context reading
// a gate's verdict), not a precondition for gating.
func loadGatesMap(cmd *cobra.Command, store sessionstate.Store) map[string]natures.GateState {
	out := map[string]natures.GateState{}
	if store == nil {
		return out
	}
	entries, err := store.ListState(gatesGuardrailKey, gateStatePrefix)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: gate verdicts unavailable, treating as none: %v\n", err)
		return out
	}
	for _, e := range entries {
		name := e.Key[len(gateStatePrefix):]
		var st natures.GateState
		if json.Unmarshal([]byte(e.Value), &st) != nil {
			continue
		}
		out[name] = st
	}
	return out
}

// recordGateVerdict writes one gate's verdict to the session store and updates the
// in-memory map so later gates in the same dispatch see it.
//
// Persisted under the reserved keyspace so the NEXT cycle — and a context's own
// exit, next slice — can read what this gate decided. A write failure is reported
// and swallowed: the engine's own bookkeeping going wrong is not the project's
// rule being violated, and refusing the agent's work over it would be a refusal no
// gate asked for — the same rule the old dispatch's state writes follow.
func recordGateVerdict(cmd *cobra.Command, store sessionstate.Store, gatesMap map[string]natures.GateState, name string, status natures.GateStatus) {
	st := natures.GateState{Status: status}
	gatesMap[name] = st
	if store == nil {
		return
	}
	value, err := json.Marshal(st)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: gate verdict not recorded: %v\n", err)
		return
	}
	if err := store.SetState(gatesGuardrailKey, gateStatePrefix+name, string(value)); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: gate verdict not recorded: %v\n", err)
	}
}

// loadContextMap reads the context[] state map for the runner and a gate trigger's
// `match`.
//
// For THIS slice it is whatever context state exists — empty until the context
// slice writes it. It is loaded through the same store the gates map is, so when
// that slice lands the source changes here in one place and the gate dispatch
// reads populated contexts without further change. Returns an empty (non-nil) map
// so a `{context}` prerequisite reads a defined-but-inactive world rather than a
// nil.
func loadContextMap(_ *cobra.Command, _ sessionstate.Store) map[string]natures.ContextState {
	// The context lifecycle (enter/exit writing this map) is the next slice. Until
	// then there is no context state to read, so this is empty — which a
	// `{context}` prerequisite correctly reads as "the context has not entered".
	return map[string]natures.ContextState{}
}
