// Package e2e covers the matcher half of "a mechanism that fails must not read
// as approval".
//
// 004 establishes it for hooks: a hook that exits non-zero, that cannot be run,
// or that says nothing at all still refuses. The matcher side had no equivalent.
// A matcher erroring at run time skipped its binding — the rule was not asked,
// the hook never ran, and the action proceeded with the reason on a stderr
// channel that reaches nobody at this hook point.
//
// A matcher that cannot be evaluated is the engine being unable to ANSWER
// whether the rule applies. That is not the rule being satisfied, and the two
// were indistinguishable from outside.
//
// # Why an error is still possible after the zero-value fill-in
//
// The fill-in supplies a declared field the producer omitted, at the shape the
// declaration promised, all the way down. It closes every gap the declaration
// knows about. It cannot close the ones it does not: a list whose element shape
// a module did not declare leaves its predicate body unchecked, so an expression
// reaching inside an element is checked against nothing at load and meets
// whatever actually arrives.
//
// The command module declares `invocations` exactly that way, so this is not a
// contrived case — it is the one field shape in the shipped build where an
// authored rule can still error.
package e2e

import (
	"strings"
	"testing"
)

// A rule about `npm publish --access <value>`: does the invocation carry an
// access flag with a value? `invocations` is declared a list with no element
// shape, so the predicate body compiles unchecked, and `.flags.access` is nil
// on any command that did not pass the flag — which `len` then refuses.
//
// An ordinary rule to write, phrased the ordinary way. Nothing about it warns
// the author it will error on the commands it is meant to let past.
const matcherErrorsAtRuntime = `---
hooks:
  PreCommandInvoke:
    - matcher: any(invocations, len(.flags.access) > 0)
      hooks:
        - type: command
          command: ./refuse.sh
---

# Refuses an npm publish that names an access level
`

// The same guardrail with a matcher that evaluates cleanly. The control: it
// shows the binding does fire and refuse when the matcher can be answered, so a
// refusal in the test above is the error path rather than this rule matching.
const matcherEvaluatesCleanly = `---
hooks:
  PreCommandInvoke:
    - matcher: any(invocations, .bin == "npm")
      hooks:
        - type: command
          command: ./refuse.sh
---

# Refuses any npm invocation
`

// A guardrail bound to writes, used to show an erroring matcher on one event
// does not spill onto another.
const bindEveryWrite = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./refuse.sh
---

# Refuses whatever it is shown
`

const refuseScript = "#!/bin/sh\ncat >/dev/null\necho 'the hook refused' >&2\nexit 1\n"
const permitScript = "#!/bin/sh\ncat >/dev/null\nexit 0\n"

// permitted reports whether the action went through.
func permitted(output string) bool {
	for _, sign := range []string{"deny", "denied", "block", "blocked"} {
		if strings.Contains(output, sign) {
			return false
		}
	}
	return true
}

// T014_01: a matcher that errors at run time refuses the action.
//
// The finding. `session_pre_tool.go` printed the error to stderr and skipped the
// binding, and at this hook point stderr beside a permitted action reaches
// neither the agent nor the transcript — so the rule was not consulted, nothing
// refused, and nothing said so. The command runs and the project believes it was
// checked.
//
// The hook here PERMITS, so a refusal cannot come from it: the only thing that
// can refuse this command is the engine's response to the matcher error.
func TestT014_01_MatcherErrorRefusesTheAction(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "npm-access", matcherErrorsAtRuntime, map[string]string{
		"refuse.sh": permitScript,
	})

	got := e.Run(proj, "s-014-01", "publish the package", Turns("done",
		Bash("b1", "npm publish"),
	))

	if permitted(got.Output) {
		t.Fatalf("a matcher that could not be evaluated permitted the command:\n%s", got.Output)
	}
	if !got.Saw("npm-access") {
		t.Errorf("the refusal does not name the guardrail whose matcher failed:\n%s", got.Output)
	}
	// The author has to be able to find the expression that failed, or the
	// refusal is a wall with no door in it.
	if !got.Saw("flags.access") {
		t.Errorf("the refusal does not quote the matcher that could not be evaluated:\n%s", got.Output)
	}
}

// T014_02: the control. The same guardrail with an answerable matcher refuses
// through its hook, in the hook's own words.
//
// Without this, T014_01 passes for an engine that refuses every command, and the
// two cannot be told apart.
func TestT014_02_AnAnswerableMatcherStillRefusesThroughItsHook(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "npm-any", matcherEvaluatesCleanly, map[string]string{
		"refuse.sh": refuseScript,
	})

	got := e.Run(proj, "s-014-02", "publish the package", Turns("done",
		Bash("b1", "npm publish"),
	))

	if permitted(got.Output) {
		t.Fatalf("a matcher that evaluates cleanly did not reach its hook:\n%s", got.Output)
	}
	if !got.Saw("the hook refused") {
		t.Errorf("the hook's own reason did not reach the agent:\n%s", got.Output)
	}
}

// T014_03: the other half — an answerable matcher that declines still permits.
//
// Failing closed everywhere would satisfy T014_01 and be useless. A rule whose
// matcher was asked and answered "no" must let the work through, and only this
// tells the fix apart from a blanket refusal.
func TestT014_03_AMatcherThatDeclinesStillPermits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "npm-any", matcherEvaluatesCleanly, map[string]string{
		"refuse.sh": refuseScript,
	})

	got := e.Run(proj, "s-014-03", "list the files", Turns("done",
		Bash("b1", "ls -la"),
	))

	if !permitted(got.Output) {
		t.Fatalf("a matcher that answered 'no' was treated as failing:\n%s", got.Output)
	}
}

// T014_04: an erroring matcher on one event does not refuse a different one.
//
// The scope of the refusal is the binding that could not be evaluated. A rule
// about commands whose matcher errors must not start blocking file writes — that
// would turn one unanswerable expression into a project-wide halt, which is the
// mirror of the bug rather than a fix for it.
func TestT014_04_AnErroringMatcherDoesNotBlockAnotherEvent(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "npm-access", matcherErrorsAtRuntime, map[string]string{
		"refuse.sh": permitScript,
	})

	got := e.Run(proj, "s-014-04", "write a note", Turns("done",
		Write("w1", "any/notes.md", "hello"),
	))

	if !permitted(got.Output) {
		t.Fatalf("a command rule's matcher error blocked a write it was never about:\n%s", got.Output)
	}
}

// T014_05: a sound rule beside an erroring one still speaks in its own voice.
//
// The erroring matcher refuses, but a guardrail that CAN answer and does refuse
// must still produce its own hook's reason rather than being replaced by the
// engine's diagnostic. Both are refusals; they are not the same refusal.
func TestT014_05_ASoundRuleKeepsItsOwnReason(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "npm-access", matcherErrorsAtRuntime, map[string]string{
		"refuse.sh": permitScript,
	})
	e.Guardrail(proj, "writes", bindEveryWrite, map[string]string{
		"refuse.sh": refuseScript,
	})

	got := e.Run(proj, "s-014-05", "write a note", Turns("done",
		Write("w1", "any/notes.md", "hello"),
	))

	if permitted(got.Output) {
		t.Fatalf("the sound rule did not refuse:\n%s", got.Output)
	}
	if !got.Saw("the hook refused") {
		t.Errorf("the sound rule's own reason was replaced:\n%s", got.Output)
	}
}
