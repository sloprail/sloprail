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
	"github.com/sloprail/sloprail/internal/harness"
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
	scope := hookScope{}
	if p.Cwd != "" {
		scope.Workspace = workspaceAnchor(p.Cwd)
	}
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

// newNatureDeclarations loads the new-format declarations in force for a session:
// the project's own under `.sloprail`, PLUS those shipped by the plugins the
// project has enabled. Any that could not be loaded are reported on the same
// channel the old format uses, as are unresolved plugins and shadowed declarations.
//
// # Plugin resolution
//
// The enabled-plugin set is resolved through internal/harness: it reads the
// project's own `.claude/settings.json` and `settings.local.json`, locates each
// enabled plugin's installation, and returns its root. Those roots become
// declaration.Origins, and declaration.NewWithPlugins reads each plugin's own
// `.sloprail` alongside the project's. harness.Resolve is harness-generic plugin
// discovery, so a plugin's new-format rules are found the same way the harness
// finds everything else a plugin ships.
//
// # Fail-open parity with the old dispatch
//
// Every failure here is REPORTED and treated as "no plugin declarations", never as
// a refusal, matching the old dispatch's stance:
//
//   - No home directory (the install cache cannot be located): reported, and the
//     load falls back to the project's own declarations only. The old path returns
//     the error to its caller; the new dispatch has no error channel to its hook
//     wrapper, so it degrades to project-only and says so — the same "half the
//     rules is worse than none of the plugin's" tension, resolved toward keeping
//     the project's own rules live rather than dropping everything.
//   - harness.Resolve error (a settings file that exists but cannot be read or
//     parsed): reported, project-only. guardrail's own path refuses the action on
//     this; here the new dispatch reports and runs project-only so a broken
//     settings file does not disarm the project's own new-format rules, which the
//     old format's dispatch is simultaneously enforcing from the same settings.
//   - Unresolved enabled plugins (a plugin the project enabled whose files were not
//     found): reported LOUDLY via reportUnresolved — the same call the old path
//     makes — so a plugin whose guardrails silently vanished is named on the next
//     tool call rather than never. This is the whole safety property of reading a
//     harness's configuration from in here.
//   - An unreadable store (a `.sloprail` directory that will not list): reported
//     and treated as empty, since a directory that gives the engine nothing to
//     enforce must not make it refuse every action the agent cannot fix.
//
// A registry is required so trigger matches can be evaluated — the loader compiles
// them against the module vocabulary.
func newNatureDeclarations(cmd *cobra.Command, cwd string, reg *module.Registry) declaration.Loaded {
	store, unresolved := natureDeclarationStore(cmd, cwd)
	// Reported here, once per load, the same as reportUnresolved is called at every
	// old-format hook point — a plugin that could not be located is named on every
	// dispatch, not only at a session start nobody was watching.
	reportUnresolved(cmd, unresolved)

	loaded, err := store.Load(reg)
	if err != nil {
		// The store itself could not be read (an unreadable `.sloprail`, or a
		// config.yaml that exists and cannot be parsed). Reported and treated as
		// empty, the same as the old dispatch does for an unreadable guardrails
		// directory: an engine deciding on its own that nothing was declared is the
		// fail-open this codebase closes elsewhere, but a directory that will not
		// list gives it nothing to enforce and refusing every action punishes a
		// fault the agent cannot fix.
		fmt.Fprintf(cmd.ErrOrStderr(),
			"sloprail: the new-format declarations in this project could not be read: %v\n", err)
		return declaration.Loaded{}
	}
	reportNatureInvalid(cmd, loaded.Invalid)
	reportNatureShadowed(cmd, loaded.Shadowed)
	reportScopeOverlaps(cmd, loaded.ScopeOverlaps)
	return loaded
}

// natureDeclarationStore builds the plugin-aware declaration store for a session,
// resolving the enabled plugins through internal/harness, and returns the
// unresolved plugins alongside so the caller can report them.
//
// On any resolution failure it returns a PROJECT-ONLY store (the project's own
// `.sloprail`, no plugins) rather than nil, so the caller always has a store to
// load and the project's own new-format rules keep enforcing even when plugin
// discovery could not run. The failure is reported here; the empty store is what
// the caller proceeds with.
func natureDeclarationStore(cmd *cobra.Command, cwd string) (*declaration.Store, []harness.Unresolved) {
	home, err := os.UserHomeDir()
	if err != nil {
		// Without a home directory the install cache cannot be found. Reported, and
		// the load proceeds with the project's own declarations — a session that
		// enforces the project's own rules is better than one that enforces nothing
		// because it could not find the plugin cache.
		fmt.Fprintf(cmd.ErrOrStderr(),
			"sloprail: plugin-shipped new-format declarations not loaded (no home directory to locate the plugin cache): %v\n", err)
		return declaration.New(dotDir(cwd)), nil
	}

	res, err := harness.Resolve(projectDir(cwd), home)
	if err != nil {
		// A settings file that exists and cannot be read or parsed. Reported, and
		// the load proceeds project-only: the dispatch keeps the project's own
		// new-format rules live rather than disarming them over a settings file it
		// could not read to discover plugins.
		fmt.Fprintf(cmd.ErrOrStderr(),
			"sloprail: plugin-shipped new-format declarations not loaded (the project's plugin settings could not be read): %v\n", err)
		return declaration.New(dotDir(cwd)), nil
	}

	plugins := make([]declaration.Origin, 0, len(res.Roots))
	for _, r := range res.Roots {
		plugins = append(plugins, declaration.Origin{Plugin: r.Plugin.Name, Root: r.Dir})
	}
	return declaration.NewWithPlugins(dotDir(cwd), plugins), res.Unresolved
}

// reportNatureInvalid names every new-format declaration that could not be loaded,
// one line per fault, then how to get unstuck — the same shape reportInvalid uses
// for the old format.
//
// Reported rather than fatal: a broken declaration blocks nothing (it dispatches
// nothing below), but is named every time so an author fixing it sees all of it.
// The channel is stderr, which reaches a person tailing logs; a refusal is not
// raised for a rule that could not load, matching the old format's settled rule.
//
// Each report ends with iv.Remedy() — origin-aware repair guidance restored from
// the old format's remedy. A project's own broken rule is theirs to fix or
// disable; a PLUGIN's rule is not (its file is in an install cache the next
// reinstall overwrites), so its remedy is the `disabled: [<qualified>]` line in
// the project's own config — the same mechanism the sibling shadow report quotes,
// worded once on the Invalid so the two diagnostics agree.
func reportNatureInvalid(cmd *cobra.Command, invalid []declaration.Invalid) {
	for _, iv := range invalid {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: declaration %s not loaded:\n", iv.Attribution())
		for _, reason := range iv.Reasons {
			fmt.Fprintf(cmd.ErrOrStderr(), "  - %s\n", reason)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", iv.Remedy())
	}
}

// reportNatureShadowed says which plugin-shipped new-format declarations a
// declaration of the same (nature, name) displaced — the project's own, or an
// earlier plugin's.
//
// Reported at the same point the invalid set is, for the reason guardrail's
// reportShadowed is: a project that displaced a shipped rule and was never told
// believes it has two protections and has one. Stderr is a weak channel beside a
// PERMITTED action (it reaches no agent at exit 0, per the table in refuseForBroken),
// and that is accepted rather than escalated: shadowing is not a broken rule — both
// declarations are well-formed and the winner is enforcing — and refusing every
// action because a project overrode a rule would make overriding impossible, which
// is the capability the mechanism exists to provide. The warning belongs where a
// person looking for it will find it.
func reportNatureShadowed(cmd *cobra.Command, shadowed []declaration.Shadow) {
	for _, sh := range shadowed {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: %s\n", sh.Message())
	}
}

// reportScopeOverlaps names every pair of plugin structure gates whose literal
// scopes overlap. Both stay loaded and a write inside the overlap is refused as
// an ownership conflict; this report is what lets a person see the conflict and
// switch one off before an agent meets it. Stderr, with the shadow report, for
// the same reason: it is a warning about configuration, not a refusal.
func reportScopeOverlaps(cmd *cobra.Command, overlaps []declaration.ScopeOverlap) {
	for _, o := range overlaps {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: %s\n", o.Message())
	}
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
	contextMap map[string]natures.ContextState,
	gatesMap map[string]natures.GateState,
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
		fired, ok, err := firstMatchingEvent(cmd, reg, g, events, contextMap)
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
			recordGateVerdict(cmd, store, gatesMap, g.Name, natures.GateStatusFail)
			continue
		}
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
			Workspace:      scope.Workspace,
			SessionID:      scope.SessionID,
			// The re-entry provenance to hand a check that spawns sr-agent: this
			// gate appended to whatever launched checks are already on the stack.
			// So a judge check's own agent, whose Write re-fires this dispatch,
			// finds this gate in SLOPRAIL_LAUNCHED_BY and is not re-enforced by it
			// (isLaunchedBy above, one exec down). appendLaunchedBy dedups and keeps
			// any outer entry, so a nested launch carries the whole chain.
			LaunchedBy: appendLaunchedBy(os.Getenv, g.Name),
		})
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
			results = append(results, gateResult{Name: g.Name, Attribution: g.Attribution(), Refused: true, Reason: verdict.Reason})
		}
	}
	return results
}

// firstMatchingEvent returns the first fired event a gate's `on` triggers match,
// whether any did, and an error when a trigger's match could not be decided.
//
// A gate wakes when one of its triggers names an event kind that fired AND that
// trigger's `match` (compiled against the gate scope for the kind) evaluates true.
// The first such (trigger, event) pair is what the gate decides about — a gate is
// one-shot, so it runs once even if several triggers or events would match.
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
func firstMatchingEvent(cmd *cobra.Command, reg *module.Registry, g declaration.Gate, events []event.Event, contextMap map[string]natures.ContextState) (event.Event, bool, error) {
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
				// Compile disagreeing with load: fail closed. The trigger match is
				// quoted in the error so the refusal an author sees points at the
				// expression to fix.
				return event.Event{}, false, fmt.Errorf("its trigger match %q on %s could not be compiled (%w)", trig.Match, trig.Event, err)
			}
			ok, err := m.Match(gateMatchEvent(e, contextMap))
			if err != nil {
				// The match compiled but could not be EVALUATED against this event.
				// Fail closed, quoting the trigger match.
				return event.Event{}, false, fmt.Errorf("its trigger match %q on %s could not be evaluated (%w)", trig.Match, trig.Event, err)
			}
			if ok {
				return e, true, nil
			}
		}
	}
	return event.Event{}, false, nil
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
