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
//
// The same limit covers the collection-time `enabled` filter in
// runSessionPreTool, for the same reason: with the dispatch-time filter still in
// place, deleting it changes only which modules run, and a disabled guardrail's
// hook stays unrun either way. Deleting the DISPATCH filter alone is observable,
// and 011's T011_03 is the test that catches it.

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

// bindCommandsMatchingNothing DOES bind PreCommandInvoke, under a matcher no
// command line in this directory can satisfy.
//
// This is the detector T007_02 needs, and the reason it differs from
// bindOnlyFiles. The binding exists, so a command event produced here has
// somewhere to land and the ledger can register it. What makes producing one
// pointless is the matcher: nothing the fixture runs begins with that prefix, so
// the walk's result is discarded whatever it finds. The file bindings are kept
// unmatched-but-present for the same reason they are in bindOnlyFiles — an event
// of any kind reaching this guardrail is visible.
//
// The contrast with bindOnlyFiles is the point. There, a command event has
// nowhere to land either way and the empty ledger means nothing. Here, the
// ledger is empty only because no command event arrived.
const bindCommandsMatchingNothing = `---
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
    - matcher: raw startsWith "no-command-in-this-test-starts-like-this "
      hooks:
        - type: command
          command: ./record.sh
---

# Asks about commands, under a matcher nothing here can satisfy
`

// theCommand is the command line every test in this directory runs.
//
// Chosen, not incidental. It must be a line the command module can walk into a
// PreCommandInvoke event, and its own success or failure must not matter: the
// event fires BEFORE invocation, so what the shell would go on to do with it is
// not part of what is under test. `true` is the deliberate form of that — a real
// program, resolvable, that runs and exits zero, so no test here depends on a
// command that happens to fail for a reason nothing asserts.
const theCommand = "true --access public"

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
		Bash("b1", theCommand),
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
// bindCommandsMatchingNothing is that binding — which is what distinguishes this
// from T007_03, where the command binding is absent from the enabled set rather
// than present-but-unsatisfiable.
//
// This is a weaker probe than T007_01's control and it is meant to be — it
// catches an engine that produces command events for a project that will never
// act on one. T007_03 covers the `enabled` half.
func TestT007_02_CommandModuleNotRunForARuleThatCannotFire(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "watcher", bindCommandsMatchingNothing, map[string]string{"record.sh": recordScript})

	e.Run(proj, "s-007-02", "run something", Turns("done",
		Bash("b1", theCommand),
	))

	// Asserted on the whole ledger, not from inside a loop over it. Both claims
	// below are about an EMPTY ledger, and a loop body cannot state them: with
	// nothing to iterate, a `for` over the kinds never executes and every
	// assertion inside it is unreachable — the shape this test had, which passed
	// by never running its own checks.
	kinds := kindsSeen(t, e.Ledger(proj, "watcher", "seen"))
	for _, k := range kinds {
		if strings.HasPrefix(k, "PreCommand") {
			t.Errorf("a project whose only command rule admits nothing was handed %q", k)
		} else {
			t.Errorf("a bash turn produced file event %q, which no file module should report", k)
		}
	}
	if len(kinds) != 0 {
		t.Fatalf("nothing should have reached this guardrail's hook, got %v", kinds)
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
		Bash("b1", theCommand),
	))

	if runs := e.Ledger(proj, "off", "seen"); len(runs) != 0 {
		t.Errorf("a disabled guardrail's binding was dispatched to %d times: %v", len(runs), kindsSeen(t, runs))
	}
	if kinds := kindsSeen(t, e.Ledger(proj, "on", "seen")); len(kinds) != 0 {
		t.Errorf("only a disabled rule bound to commands, yet events were produced: %v", kinds)
	}
}
