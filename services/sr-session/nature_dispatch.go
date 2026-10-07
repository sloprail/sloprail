package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/checkrun"
	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/natures"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/srevents"
	"github.com/sloprail/sloprail/internal/transcript"
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

// natureDispatchPreTool resolves the hook scope, runs the new-format pre-tool
// dispatch against the session store it is handed, and returns the reason to deny
// (or "").
//
// The store is the caller's, opened once per tool call (natureStore) and shared
// with the baseline bookkeeping runSessionPreTool does before dispatch, so a tool
// call opens the session's database once rather than twice. It is a convenience
// for the gates[] map (a gate still decides without it); nil — a store that would
// not open, already reported by natureStore — runs the dispatch storeless.
func natureDispatchPreTool(cmd *cobra.Command, p HookPayload, reg *module.Registry, store sessionstate.Store) string {
	scope := natureHookScope(cmd, p)
	return dispatchNaturePreTool(cmd, p, reg, scope, store).Blocked
}

// natureDispatchStop resolves the scope and store, runs the new-format Stop
// dispatch, and returns the text to block the turn with (or "").
//
// The Stop counterpart of natureDispatchPreTool. Unlike it, this one opens and
// closes its own store: completeCycle holds a store of its own across the
// dispatch, and the two are kept separate as they always were.
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
//
// The workspace is the tree's anchor (its git root), not the raw cwd. It reaches
// every script as SR_WORKSPACE, documented as "the repository root — prepend it
// to a `.event.path`", and event paths ARE repository-relative; after an agent's
// `cd memories/tasks`, the raw cwd made `$SR_WORKSPACE/$path` name a file that
// does not exist. An empty cwd stays empty so workspaceEnv still sets the
// unresolved sentinel rather than an anchor guessed from this process's own
// directory.
func natureHookScope(cmd *cobra.Command, p HookPayload) hookScope {
	scope := hookScope{ToolUseID: p.ToolUseID, AgentID: p.AgentID}
	if p.Cwd != "" {
		scope.Workspace = workspaceAnchor(p.Cwd)
	}
	if id, err := stableIdentity(p); err == nil {
		// A fallback identity is reported at SessionStart, where it is seen —
		// not here, where it would be recorded and shown to nobody. See
		// noteDegradedIdentity.
		scope.SessionID = id.ID
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

// newNatureDeclarations loads the new-format declarations in force for a folder: the project's
// own under `.sloprail`, PLUS those shipped by the plugins the project has enabled. Failures
// are reported on the command's stderr and never refuse (see checkrun.LoadDeclarations).
// sr:invariant gates/broken-declaration-denies-nothing
func newNatureDeclarations(cmd *cobra.Command, cwd string, reg *module.Registry, sessionStart ...string) declaration.Loaded {
	return checkrun.LoadDeclarations(cmd.ErrOrStderr(), cwd, reg, sessionStart...)
}

// natureDeclarationStore builds the plugin-aware declaration store for a folder; on any
// resolution failure a PROJECT-ONLY store (see checkrun.DeclarationStore).
func natureDeclarationStore(cmd *cobra.Command, cwd string) (*declaration.Store, []harness.Unresolved) {
	return checkrun.DeclarationStore(cmd.ErrOrStderr(), cwd)
}

// gateResult is one gate's outcome on one fired event: the gate's name, how a
// refusal should attribute it, whether it refused, and the reason to relay.
//
// Attribution carries the plugin-aware name (the bare name for a project's gate,
// the name plus " from plugin X" for a shipped one), so a refusal names where a
// gate a project never wrote actually lives — the same reason the old format
// attributes a refusal by Origin. Name stays for the diagnostics that key on the
// bare folder name (the re-entry guard, the state map).
type gateResult struct {
	Name        string
	Attribution string
	Refused     bool
	Reason      string
	// Path is the file a refusal on a Pre file event is about; "" for a refusal
	// that is about the whole call (a command, a tool, Stop, or a trigger that
	// could not be decided).
	Path string
}

// runGatesForEvents runs every gate whose `on` trigger matches one of the fired
// events, in the context of the session, and returns the results in gate-name
// order.
//
// This is the shared body both hook points call — pre-tool with its pre-events,
// Stop with the Stop event. A gate is woken by the triggers that match a fired
// event, and the check-runner then evaluates require + checks per event and
// produces the verdict, which is recorded into the gates[] map here so a later
// cycle or a context can read it.
//
// # Every file a call changes is asked about
//
// One tool call can change several files (`rm a.go b.go`, `sed -i … a b`, two
// sr-file calls joined by &&) and the call runs whole or not at all. So a gate is
// run once per matching PRE FILE event, not only for the first: a gate that
// passed the first file and was never asked about the second would admit the
// second's not-fine change. Each refused file is its own result carrying its
// Path, so the one deny can name every file to fix. Every gate is asked about every
// file it selects, whether or not another gate refused it (a gate's ledger and
// verdict are its own); gateRefusal names the first refusal per file. Every other
// event kind — a command, a tool, Stop — is one-shot: the first matching event
// wakes the gate once.
//
// notes is what a pure sr-file line's dry run said about a change it could not
// compute (nil at Stop): it is quoted with a refusal of that uncomputed change so
// the agent hears sr-file's own reason rather than guessing.
//
// The context[]/gates[] maps read by the runner come from the store (loadGatesMap)
// — for THIS slice the context map is whatever context state exists (empty until
// the context slice populates it), threaded through so that slice changes only the
// map's source, not this dispatch.
// sr:invariant matching/unevaluable-never-passes
// sr:invariant gates/multi-file-call-refused-whole
func runGatesForEvents(
	cmd *cobra.Command,
	reg *module.Registry,
	gates []declaration.Gate,
	events []event.Event,
	scope hookScope,
	store sessionstate.Store,
	contextMap map[string]natures.ContextState,
	gatesMap map[string]natures.GateState,
	notes resolveNotes,
) []gateResult {
	if len(gates) == 0 {
		return nil
	}

	// The state maps are LOADED BY THE CALLER and threaded in, so contexts,
	// gates and file-guards in one dispatch all read one consistent world — a
	// context that entered on a Post event this cycle is visible to a gate's
	// `require: [{context}]` here, because the orchestrator ran the enters first
	// and passes the populated map. gatesMap is read and written back per verdict.
	runner := dispatchcore.Runner{}
	var results []gateResult

	for _, g := range gates {
		// sr:invariant gates/undecidable-gate-refuses
		fired, err := matchingEvents(cmd, reg, g, events, contextMap)
		if err != nil {
			// A trigger's match could not be COMPILED or EVALUATED. That is not the
			// gate cleanly not waking — it is the engine unable to answer whether the
			// trigger applies, and treating it as a non-wake would silently DISABLE the
			// gate on a match it could not confirm (the fail-open this regressed to).
			// Fail CLOSED: refuse, naming the gate and quoting the trigger match, the
			// same direction the runner-error branch below and the old dispatch
			// (matcher.go:121/186) take. A gate whose trigger cannot be evaluated must
			// not be read as approval of the event it was bound to.
			fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: gate %s: %v\n", g.Attribution(), err)
			results = append(results, gateResult{
				Name:        g.Name,
				Attribution: g.Attribution(),
				Refused:     true,
				Reason:      fmt.Sprintf("the gate %s could not be evaluated (%v); refusing because a gate that could not decide must not be read as approval", g.Attribution(), err),
			})
			srevents.Emit(srevents.Event{Kind: srevents.GateChecked, Rule: srevents.Rule(g.Origin.Plugin, g.Name),
				Outcome: srevents.Refused, ToolUseID: scope.ToolUseID, Reason: err.Error()})
			recordGateVerdict(cmd, store, gatesMap, g.Name, natures.GateStatusFail)
			continue
		}
		if len(fired) == 0 {
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

		status := natures.GateStatusPass
		for _, e := range fired {
			path := ""
			if isPreFileEvent(e.Kind) {
				path = displayPath(eventPath(e), scope.Workspace)
			}

			verdict, err := runner.Run(dispatchcore.Request{
				Nature:         dispatchcore.NatureGate,
				Require:        g.Require,
				Checks:         g.Checks,
				Event:          e,
				TranscriptPath: scope.Transcript,
				Context:        contextMap,
				Gates:          gatesMap,
				Dir:            g.Dir,
				GuardName:      g.Name,
				Qualified:      g.Qualified(),
				Workspace:      scope.Workspace,
				SessionID:      scope.SessionID,
				AgentID:        scope.AgentID,
				// The re-entry provenance to hand a check that spawns sr-agent: this
				// gate appended to whatever launched checks are already on the stack.
				// So a judge check's own agent, whose Write re-fires this dispatch,
				// finds this gate in SLOPRAIL_LAUNCHED_BY and is not re-enforced by it
				// (isLaunchedBy above, one exec down). appendLaunchedBy dedups and keeps
				// any outer entry, so a nested launch carries the whole chain.
				LaunchedBy: appendLaunchedBy(os.Getenv, g.Name),
			})
			// sr:invariant gates/undecidable-gate-refuses
			emitGate(g, e, scope, verdict, err)
			if err != nil {
				// The runner itself could not decide (a programming error, not a check
				// refusal — the runner turns a check that cannot run into a refusal
				// rather than an error). Fail-closed: refuse, naming the gate.
				fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: gate %s: %v\n", g.Attribution(), err)
				results = append(results, gateResult{
					Name:        g.Name,
					Attribution: g.Attribution(),
					Refused:     true,
					Reason:      fmt.Sprintf("the gate %s could not be evaluated (%v); refusing because a gate that could not decide must not be read as approval", g.Attribution(), err),
					Path:        path,
				})
				status = natures.GateStatusFail
				continue
			}
			if !verdict.Refused {
				continue
			}
			status = natures.GateStatusFail
			reason := verdict.Reason
			if isUnderivablePreWrite(e) {
				// A change the engine could not compute: when sr-file's dry run said
				// why, that is worth more than the check's own words.
				if note := notes.For(eventPath(e)); note != "" {
					reason = withResolveNote(reason, note)
				}
			}
			results = append(results, gateResult{Name: g.Name, Attribution: g.Attribution(), Refused: true, Reason: reason, Path: path})
		}
		recordGateVerdict(cmd, store, gatesMap, g.Name, status)
	}
	return results
}

// withResolveNote adds what sr-file said about a change it could not compute to
// a refusal of that change. Its words already carry the sub-agent advice when it
// applies, so the refusal's own copy of that advice is dropped.
func withResolveNote(reason, note string) string {
	if strings.Contains(note, transcript.SubagentUserAdvice) {
		reason = strings.TrimSuffix(reason, "\n"+transcript.SubagentUserAdvice)
	}
	return fmt.Sprintf("%s sr-file said:\n%s", reason, note)
}

// matchingEvents returns the fired events a gate's `on` triggers match, and an
// error when a trigger's match could not be decided.
//
// A gate wakes on an event when one of its triggers names that event's kind AND
// that trigger's `match` (compiled against the gate scope for the kind) evaluates
// true. Every matching PRE FILE event is returned, in the order fired, so the
// dispatch asks the gate about each file a call changes. For every other kind the
// first matching event only is returned — the gate is one-shot on a command, a
// tool or Stop.
//
// A trigger's match is compiled here rather than at load because the KIND the
// event carries is needed to build the scope — the same reason the old dispatch
// compiles a matcher at the hook point.
//
// # A match that cannot be decided FAILS CLOSED
//
// A compile failure (unreachable for a loaded gate, whose triggers the loader
// already compiled) or an EVALUATION error (a trigger whose `match` compiled but
// erred on the event handed to it — e.g. `int(.bin)` on "npm", which the vm
// refuses) is NOT treated as "this trigger did not match". Doing so would let a
// broken or adversarial trigger silently DISABLE the gate: the engine could not
// confirm the match, and reading that as a non-wake reads it as approval of the
// event the gate was bound to. Instead the error is returned to the caller, which
// turns it into a REFUSAL naming the gate — the same fail-closed direction the old
// dispatch keeps (internal/guardrail/matcher.go:121 refuses the events a broken
// rule was bound to; :186 the caller refuses and says why). The (trigger, kind)
// that could not be decided is named in the error so the refusal can quote it.
//
// The FIRST such failure short-circuits: a gate that cannot decide one of its
// triggers cannot be said to have cleanly not matched, so it refuses rather than
// hunting for a later trigger that might wake it — a broken trigger is a fault to
// surface, not a condition to route around.
// sr:invariant matching/unevaluable-never-passes
func matchingEvents(cmd *cobra.Command, reg *module.Registry, g declaration.Gate, events []event.Event, contextMap map[string]natures.ContextState) ([]event.Event, error) {
	matched := make([]bool, len(events))
	for _, trig := range g.On {
		// The trigger's `event` may be the PreFileWrite alias; expand it to the
		// concrete kinds it fires on, the same table the loader validated it
		// against. A trigger that loaded is always known.
		kinds, _ := declaration.ExpandGateEvent(trig.Event)
		for i, e := range events {
			if !containsKind(kinds, e.Kind) {
				continue
			}
			kindDecl, known := reg.KindDeclFor(e.Kind)
			if !known {
				continue
			}
			m, err := guardrail.CompileGateMatch(trig.Match, kindDecl)
			if err != nil {
				// Compile disagreeing with load: fail closed. The trigger match is
				// quoted in the error so the refusal an author sees points at the
				// expression to fix.
				return nil, fmt.Errorf("its trigger match %q on %s could not be compiled (%w)", trig.Match, trig.Event, err)
			}
			ok, err := m.Match(gateMatchEvent(e, contextMap))
			if err != nil {
				// The match compiled but could not be EVALUATED against this event.
				// Fail closed, quoting the trigger match.
				return nil, fmt.Errorf("its trigger match %q on %s could not be evaluated (%w)", trig.Match, trig.Event, err)
			}
			if ok {
				matched[i] = true
			}
		}
	}
	var out []event.Event
	sawOther := false
	for i, e := range events {
		if !matched[i] {
			continue
		}
		if isPreFileEvent(e.Kind) {
			out = append(out, e)
			continue
		}
		if !sawOther {
			sawOther = true
			out = append(out, e)
		}
	}
	return out, nil
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
// event's own fields under `event`, so `event.path` / `event.invocations` resolve,
// and the context[] map (wire form) under `context`, so a gate trigger MAY narrow
// on `context[<name>]` the way its GateMatchScope declares.
//
// This is the runtime env shape CompileGateMatch documents — a gate scope nests
// where a file scope is flat. The context map is now threaded (the context slice
// populates it), in the wire form an expression indexes (contextMatchValue), so a
// trigger reading `context["x"].active` evaluates against the real state rather
// than an undefined value. Every declared context is present in the map (seeded
// inactive), so an inactive one reads false rather than erroring on an absent key.
func gateMatchEvent(e event.Event, contextMap map[string]natures.ContextState) event.Event {
	return event.Event{Kind: e.Kind, Fields: map[string]any{
		"event":   e.Fields,
		"context": contextMatchValue(contextMap),
	}}
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

// loadContextMap now lives in nature_context.go, where the context lifecycle that
// writes the map also reads it — seeded so every declared context is present
// (inactive by default) rather than the empty stub this slice replaced.
