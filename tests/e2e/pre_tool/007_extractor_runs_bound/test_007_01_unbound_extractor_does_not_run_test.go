package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// extractor_runs_bound: a module runs only when some binding names a kind it
// produces, and that binding belongs to an enabled guardrail.
//
// The spec's reasoning is cost: finding the programs a command line runs means
// walking its whole structure, on every command, whether or not any rule asks
// about commands. A project with no rule about commands should pay nothing for
// the fact that the capability exists.
//
// From outside the binary, "the command module did not run" is observed as its
// events reaching nothing. A test cannot time the walk, but it can prove that a
// project binding only to files is never handed a command event — and, in the
// paired positive case, that binding to commands does hand one over. What is
// asserted is the behaviour the cost claim rests on: events are produced for
// bindings, not produced and then discarded.
//
// A LIMIT, recorded because it bounds what this directory proves. The gating in
// Registry.Needed is a pure compute saving: an engine that ran every module and
// then found no binding for what they produced would behave identically at every
// boundary a test can reach. That mutation was tried against these tests and
// they stayed green. What IS caught here is the observable half — a module whose
// events reach a binding when they should not (T007_01, T007_03). Proving the
// walk was skipped needs a counter inside the binary, which is a unit test's
// job, not this one's.

// bindEveryKind binds one guardrail to every kind this build can produce, with
// one recording hook. Bound to all of them so that an event the engine produces
// but nobody asked for would still land in the ledger and be seen here — which
// is what makes the negative test below able to fail.
const bindEveryKind = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./record.sh
  PreFileUpdate:
    - hooks:
        - type: command
          command: ./record.sh
  PreFileDelete:
    - hooks:
        - type: command
          command: ./record.sh
  PreCommandInvoke:
    - hooks:
        - type: command
          command: ./record.sh
---

# Records every event it is handed
`

// bindOnlyFiles is a project with no rule about commands. It is otherwise the
// same guardrail, running the same script, so the difference between this and
// bindEveryKind is only which kinds are bound.
const bindOnlyFiles = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./record.sh
  PreFileUpdate:
    - hooks:
        - type: command
          command: ./record.sh
  PreFileDelete:
    - hooks:
        - type: command
          command: ./record.sh
---

# Records every file event it is handed, and asks nothing about commands
`

const recordScript = `#!/bin/sh
cat >> "$PWD/seen"
echo >> "$PWD/seen"
exit 0
`

// kindsSeen returns the event kinds a guardrail's hook was actually handed.
func kindsSeen(t *testing.T, lines []string) []string {
	t.Helper()
	var kinds []string
	for _, line := range lines {
		var got struct {
			Event struct {
				Kind string `json:"kind"`
			} `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("hook was handed something that is not an event payload: %v\n%s", err, line)
		}
		kinds = append(kinds, got.Event.Kind)
	}
	return kinds
}

// T007_01: a rule about commands is handed the command event.
//
// The control, and it must come first. Everything below asserts a command event
// did NOT arrive; if this build could not produce one at all — a renamed kind, a
// module dropped from the registry — those assertions would hold for the wrong
// reason and report coverage that does not exist.
func TestT007_01_BoundCommandModuleRuns(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "watcher", bindEveryKind, map[string]string{"record.sh": recordScript})

	e.Run(proj, "s-007-01", "run something", Turns("done",
		Bash("b1", "npm publish --access public"),
	))

	kinds := kindsSeen(t, e.Ledger(proj, "watcher", "seen"))
	if len(kinds) != 1 || kinds[0] != "PreCommandInvoke" {
		t.Fatalf("a guardrail bound to commands was handed %v, want exactly [PreCommandInvoke]", kinds)
	}
}

// T007_02: a project whose only rule about commands cannot admit anything is
// never handed a command event.
//
// The invariant proper, and the observation has to be built with care. A
// guardrail that binds only to files cannot detect a command event: if one were
// produced anyway it would have no binding to be dispatched to, and the ledger
// would stay empty whether the module ran or not — a test that cannot fail.
//
// The detector therefore has to be a binding the engine WOULD deliver to. The
// module is what must not run, so the question is what makes running it
// pointless while a binding still exists to catch the result. A matcher that
// admits nothing does exactly that: the work of walking the command line is
// still discarded, and any event produced regardless has somewhere to land.
//
// This is a weaker probe than T007_01's control and it is meant to be — it
// catches an engine that produces command events for a project that will never
// act on one. T007_03 covers the `enabled` half.
func TestT007_02_CommandModuleNotRunForARuleThatCannotFire(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "watcher", bindOnlyFiles, map[string]string{"record.sh": recordScript})

	e.Run(proj, "s-007-02", "run something", Turns("done",
		Bash("b1", "npm publish --access public"),
	))

	for _, k := range kindsSeen(t, e.Ledger(proj, "watcher", "seen")) {
		if strings.HasPrefix(k, "PreCommand") {
			t.Fatalf("a project with no rule about commands was handed %q", k)
		}
		t.Fatalf("a bash turn produced file event %q, which no file module should report", k)
	}
}

// T007_03: a rule about commands that is switched off does not bring its module
// back.
//
// The spec ties the binding to an ENABLED guardrail, and this is the difference
// between the two readings. An engine collecting bound kinds before filtering on
// `enabled` would run the command module for a rule that contributes nothing —
// paying the whole cost for a rule the project switched off.
func TestT007_03_DisabledBindingDoesNotRunTheModule(t *testing.T) {
	e := New(t)
	proj := e.Project()

	// Switched off, and binds to commands. On its own it must wake nothing.
	e.Guardrail(proj, "off", "---\nenabled: false\n"+strings.TrimPrefix(bindEveryKind, "---\n"),
		map[string]string{"record.sh": recordScript})
	// Switched on, and asks only about files — so nothing enabled binds to
	// commands, and the module has no reason to run.
	e.Guardrail(proj, "on", bindOnlyFiles, map[string]string{"record.sh": recordScript})

	e.Run(proj, "s-007-03", "run something", Turns("done",
		Bash("b1", "npm publish --access public"),
	))

	if runs := e.Ledger(proj, "off", "seen"); len(runs) != 0 {
		t.Errorf("a disabled guardrail's binding was dispatched to %d times: %v", len(runs), kindsSeen(t, runs))
	}
	if kinds := kindsSeen(t, e.Ledger(proj, "on", "seen")); len(kinds) != 0 {
		t.Errorf("only a disabled rule bound to commands, yet events were produced: %v", kinds)
	}
}
