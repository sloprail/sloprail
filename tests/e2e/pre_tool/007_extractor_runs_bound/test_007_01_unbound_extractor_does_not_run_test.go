package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// extractor_runs_bound: a module runs only when some binding names a kind it
// produces, and that binding belongs to an enabled rule.
//
// The spec's reasoning is cost: finding the programs a command line runs means
// walking its whole structure, on every command, whether or not any rule asks
// about commands. A project with no rule about commands should pay nothing for the
// fact that the capability exists. This is SHARED extraction machinery — the new
// dispatch drives the SAME command module, gated by the SAME "is any bound kind
// asking" economy (services/sr-session/nature_pre_tool.go's extractPreEvents over
// reg.Needed(naturePreToolBoundKinds)).
//
// # RE-VEHICLED onto the NEW gate nature (was old GUARDRAIL.md hooks)
//
// A command occurrence is a GATE trigger (PreCommandInvoke), not a file-guard match
// — so the binding that asks about commands is a gate's `on`, and these tests
// install gates. The old rules installed via `e.Guardrail` and read the NESTED
// payload; these install NEW-format gates and read the FLAT event.
//
// # A LIMIT, unchanged from the old directory
//
// From outside the binary the gating in reg.Needed is a pure compute saving: an
// engine that ran every module and then found no binding for what they produced
// would behave identically at every boundary a test can reach. So what is caught
// here is the OBSERVABLE half — a command event reaching a binding when it should
// not, and a disabled binding not bringing its module back. Proving the walk was
// skipped needs a counter inside the binary, which is a unit test's job.

// recordCommandGate binds a gate to PreCommandInvoke and records every command
// event it is handed (to its own folder, SR_GUARDRAIL_DIR), permitting so the
// ledger reflects arrival rather than a verdict.
const recordCommandGate = `on:
  - event: PreCommandInvoke
checks:
  - script: ./record.sh
`

// recordCommandGateMatchingNothing DOES bind PreCommandInvoke, under a trigger
// match no command line in this directory can satisfy. The binding exists, so a
// command event produced here has somewhere to land and the ledger can register
// it; the match is what makes the walk's result pointless — nothing the fixture
// runs begins with that prefix. This is the detector T007_02 needs, and the reason
// it differs from a project with no command binding at all.
const recordCommandGateMatchingNothing = `on:
  - event: PreCommandInvoke
    match: event.raw startsWith "no-command-in-this-test-starts-like-this "
checks:
  - script: ./record.sh
`

// recordFileGuard binds a file-guard to markdown writes and records what it is
// handed. A file-guard never binds a command kind, so it is the "asks nothing about
// commands" side.
const recordFileGuard = `match: "**/*.md"
preventive: true
checks:
  - script: ./record.sh
`

const recordScript = `#!/bin/sh
cat >> "$SR_GUARDRAIL_DIR/seen"
echo >> "$SR_GUARDRAIL_DIR/seen"
exit 0
`

// theCommand is the command line every test in this directory runs. Chosen, not
// incidental: it must be a line the command module can walk into a PreCommandInvoke
// event, and its own success or failure must not matter (the event fires BEFORE
// invocation). `true` is the deliberate form of that — a real program, resolvable,
// that runs and exits zero.
const theCommand = "true --access public"

// gateKindsSeen returns the event kinds a gate's check was actually handed.
func gateKindsSeen(t *testing.T, lines []string) []string {
	t.Helper()
	var kinds []string
	for _, line := range lines {
		var got struct {
			Event struct {
				Kind string `json:"kind"`
			} `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("a check was handed something that is not an event payload: %v\n%s", err, line)
		}
		kinds = append(kinds, got.Event.Kind)
	}
	return kinds
}

// T007_01: a gate about commands is handed the command event.
//
// The control, and it must come first. Everything below asserts a command event did
// NOT arrive; if this build could not produce one at all — a renamed kind, a module
// dropped from the registry — those assertions would hold for the wrong reason.
func TestT007_01_BoundCommandModuleRuns(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "watcher", recordCommandGate, map[string]string{"record.sh": recordScript})

	e.Run(proj, "s-007-01", "run something", Turns("done",
		Bash("b1", theCommand),
	))

	kinds := gateKindsSeen(t, e.GateLedgerLines(proj, "watcher", "seen"))
	if len(kinds) != 1 || kinds[0] != "PreCommandInvoke" {
		t.Fatalf("a gate bound to commands was handed %v, want exactly [PreCommandInvoke]", kinds)
	}
}

// T007_02: a project whose only rule about commands cannot admit anything is never
// handed a command event.
//
// The detector has to be a binding the engine WOULD deliver to — a gate whose
// trigger match admits nothing does exactly that: the walk's result is discarded
// whatever it finds, and any event produced regardless has somewhere to land. A
// weaker probe than T007_01's control and meant to be — it catches an engine that
// delivers command events to a project that will never act on one.
func TestT007_02_CommandModuleNotDeliveredForAGateThatCannotFire(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "watcher", recordCommandGateMatchingNothing, map[string]string{"record.sh": recordScript})

	e.Run(proj, "s-007-02", "run something", Turns("done",
		Bash("b1", theCommand),
	))

	// Asserted on the whole ledger, not from inside a loop over it: both claims are
	// about an EMPTY ledger, and a loop body cannot state them (with nothing to
	// iterate, every assertion inside is unreachable).
	kinds := gateKindsSeen(t, e.GateLedgerLines(proj, "watcher", "seen"))
	for _, k := range kinds {
		if strings.HasPrefix(k, "PreCommand") {
			t.Errorf("a project whose only command rule admits nothing was handed %q", k)
		} else {
			t.Errorf("a bash turn produced event %q, which should not have reached this gate", k)
		}
	}
	if len(kinds) != 0 {
		t.Fatalf("nothing should have reached this gate's check, got %v", kinds)
	}
}

// T007_03: a rule about commands that is switched off does not bring its module
// back.
//
// The spec ties the binding to an ENABLED rule, and this is the difference between
// the two readings. A disabled gate is filtered out of the loaded set entirely
// (store.go applyDisable), so its `on: PreCommandInvoke` never enters the bound
// kinds and the command module has no reason to run — an engine collecting bound
// kinds before honouring the disable would run the module for a rule the project
// switched off. Observed as the disabled gate's own check never running, with an
// enabled file-only rule beside it so the project still loads and dispatches.
func TestT007_03_DisabledBindingDoesNotRunTheModule(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	// Switched off, and binds to commands. On its own it must wake nothing.
	e.Gate(proj, "off", recordCommandGate, map[string]string{"record.sh": recordScript})
	e.DisablePluginGuardrail(proj, "gate/off")
	// Switched on, and asks only about files — so nothing enabled binds to commands,
	// and the module has no reason to run.
	e.FileGuard(proj, "on", recordFileGuard, map[string]string{"record.sh": recordScript})

	e.Run(proj, "s-007-03", "run something", Turns("done",
		Bash("b1", theCommand),
	))

	if runs := e.GateLedgerLines(proj, "off", "seen"); len(runs) != 0 {
		t.Errorf("a disabled gate's binding was dispatched to %d times: %v", len(runs), gateKindsSeen(t, runs))
	}
	// And the enabled file rule was never handed a command event either — a bash
	// turn produces no file event for a file-guard to see.
	if kinds := fileKindsSeen(t, e.FileGuardLedgerLines(proj, "on", "seen")); len(kinds) != 0 {
		t.Errorf("only a disabled rule bound to commands, yet a file rule was handed events on a bash turn: %v", kinds)
	}
}

// fileKindsSeen returns the event kinds a file-guard's check recorded.
func fileKindsSeen(t *testing.T, lines []string) []string {
	t.Helper()
	return gateKindsSeen(t, lines) // same shape: `.event.kind`
}
