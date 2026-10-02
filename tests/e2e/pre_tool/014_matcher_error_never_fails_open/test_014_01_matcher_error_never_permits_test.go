// Package e2e covers the matcher half of "a mechanism that fails must not read
// as approval", in the NEW nature format. It is the re-vehicled 014: the pre-tool
// command path is a GATE (a command event is a gate trigger, not a file-guard
// match), so an erroring trigger `match` here is a gate that cannot decide whether
// it should wake — and a gate that cannot decide must REFUSE, not silently fail to
// wake.
//
// 004 establishes the same property for a check: a check that exits non-zero, that
// cannot be run, or that says nothing at all still refuses. The matcher side had
// no equivalent, and the NEW dispatch regressed it: a gate trigger's `match`
// erroring at run time was reported to a stderr channel that reaches nobody at the
// pre-tool hook point, and the gate was treated as simply not waking — so the rule
// was not consulted, nothing refused, and nothing said so. The command runs and
// the project believes it was checked.
//
// A matcher that cannot be evaluated is the engine being unable to ANSWER whether
// the rule applies. That is not the rule being satisfied, and the two were
// indistinguishable from outside. The fix (services/sr-session/nature_dispatch.go,
// firstMatchingEvent → runGatesForEvents) turns a trigger match error into a gate
// refusal that names the gate and quotes the expression.
//
// # Why an error is still possible after the zero-value fill-in
//
// The fill-in supplies a declared field the producer omitted, at the shape the
// declaration promised, all the way down. It closes every gap the declaration
// knows about. It cannot close the ones it does not: a MAP whose keys a module did
// not enumerate types as Any, so an accessor reaching inside it is checked against
// nothing at load and meets whatever actually arrives.
//
// The flag map used to be that place: `.flags.access` was nil on a command that
// did not pass the flag, and `len` of nil errored. It no longer is — an absent
// flag reads as an empty list (guardrail's absentListIsEmpty), because an
// ordinary flag rule erroring on every command without the flag is itself the
// fail-closed footgun. What remains is a well-typed expression the vm cannot
// answer: `int(.bin) > 0` COMPILES (int of a string is well-formed in the
// expression language) and then refuses "npm" at run time — the same shape
// 027's file-guard side rides. `event.invocations` is the gate scope's nesting
// of the kind's `invocations` field (CompileGateMatch nests the event under
// `event`).
//
// # What this suite does NOT reach
//
// The SILENT never-fires one level down — `any(event.invocations, .bni == "npm")`,
// a `.bin` typo that compiles because there is no element shape to check `.bni`
// against and then reads nil, compares unequal, and quietly does not fire — is not
// closed here and cannot be from here. commandmod now DECLARES its invocation
// element shape (bin/argv/flags), so `.bni` is caught at load the same way a
// top-level `paht` is; what T014_01 rides is an expression the vm cannot answer. Recorded so the boundary is
// explicit.
package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// A gate whose trigger match loads cleanly and cannot be answered at run time:
// `int(.bin)` on "npm" is refused by the vm. The check PERMITS, so the only thing
// that can refuse the command is the engine's answer to the matcher error.
const gateMatcherErrorsAtRuntime = `on:
  - event: PreCommandInvoke
    match: any(event.invocations, int(.bin) > 0)
checks:
  - script: ./check.sh
`

// The same gate with a trigger match that evaluates cleanly. The control: it shows
// the gate does fire and refuse when the matcher can be answered, so a refusal in
// the erroring test is the error path rather than this rule matching.
const gateMatcherEvaluatesCleanly = `on:
  - event: PreCommandInvoke
    match: any(event.invocations, .bin == "npm")
checks:
  - script: ./check.sh
`

// A gate bound to writes, used to show an erroring command matcher on one event
// does not spill onto another, and that a sound rule keeps its own voice.
const gateBindEveryWrite = `on:
  - event: PreFileWrite
checks:
  - script: ./check.sh
`

// refuseCheck refuses whatever it is shown, with its own reason on stdout so the
// refusal carries the rule's own words.
const refuseCheck = `#!/bin/sh
cat >/dev/null
echo '{"reason":"the check refused"}'
exit 1
`

// permitCheck permits whatever it is shown.
const permitCheck = `#!/bin/sh
cat >/dev/null
exit 0
`

// Whether the action went through is asked of the Result — see harness.Refused,
// which reads the harness's own refusal marker rather than scanning the stream for
// words (a path containing "deny" is not a refusal).

// T014_01: a gate trigger match that errors at run time refuses the action.
//
// The finding, re-vehicled. The new dispatch printed the error to stderr and
// treated the gate as not waking, and at this hook point stderr beside a permitted
// action reaches neither the agent nor the transcript — so the gate was not
// consulted, nothing refused, and nothing said so. The command runs and the
// project believes it was checked.
//
// The check here PERMITS, so a refusal cannot come from it: the only thing that
// can refuse this command is the engine's response to the matcher error.
func TestT014_01_MatcherErrorRefusesTheAction(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "npm-access", gateMatcherErrorsAtRuntime, map[string]string{
		"check.sh": permitCheck,
	})

	got := e.Run(proj, "s-014-01", "publish the package", Turns("done",
		Bash("b1", "npm publish"),
	))

	if got.Permitted() {
		t.Fatalf("a matcher that could not be evaluated permitted the command:\n%s", got.Output)
	}
	if !got.Saw("npm-access") {
		t.Errorf("the refusal does not name the gate whose matcher failed:\n%s", got.Output)
	}
	// The author has to be able to find the expression that failed, or the refusal
	// is a wall with no door in it.
	if !got.Saw("int(.bin)") {
		t.Errorf("the refusal does not quote the matcher that could not be evaluated:\n%s", got.Output)
	}
}

// T014_02: the control. The same gate with an answerable matcher refuses through
// its check, in the check's own words.
//
// Without this, T014_01 passes for an engine that refuses every command, and the
// two cannot be told apart.
func TestT014_02_AnAnswerableMatcherStillRefusesThroughItsCheck(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "npm-any", gateMatcherEvaluatesCleanly, map[string]string{
		"check.sh": refuseCheck,
	})

	got := e.Run(proj, "s-014-02", "publish the package", Turns("done",
		Bash("b1", "npm publish"),
	))

	if got.Permitted() {
		t.Fatalf("a matcher that evaluates cleanly did not reach its check:\n%s", got.Output)
	}
	if !got.Saw("the check refused") {
		t.Errorf("the check's own reason did not reach the agent:\n%s", got.Output)
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
	e.Gate(proj, "npm-any", gateMatcherEvaluatesCleanly, map[string]string{
		"check.sh": refuseCheck,
	})

	got := e.Run(proj, "s-014-03", "list the files", Turns("done",
		Bash("b1", "ls -la"),
	))

	if got.Refused() {
		t.Fatalf("a matcher that answered 'no' was treated as failing:\n%s", got.Output)
	}
}

// T014_04: an erroring command matcher does not refuse a different event.
//
// The scope of the refusal is the binding that could not be evaluated. A gate
// about commands whose trigger match errors must not start blocking file writes —
// that would turn one unanswerable expression into a project-wide halt, which is
// the mirror of the bug rather than a fix for it. The gate's trigger is on
// PreCommandInvoke only, so a PreFileCreate never reaches the erroring match at
// all: the engine fix must not over-broaden past the binding that failed.
func TestT014_04_AnErroringMatcherDoesNotBlockAnotherEvent(t *testing.T) {
	// The harness's own pre-Stop `sr-checks run` is a command this gate's erroring match would
	// refuse; the test is about the write alone.
	e := New(t, harness.NoAutoCheck())
	proj := e.Project()
	e.Gate(proj, "npm-access", gateMatcherErrorsAtRuntime, map[string]string{
		"check.sh": permitCheck,
	})

	got := e.Run(proj, "s-014-04", "write a note", Turns("done",
		Write("w1", "any/notes.md", "hello"),
	))

	if got.Refused() {
		t.Fatalf("a command gate's matcher error blocked a write it was never about:\n%s", got.Output)
	}
}

// T014_05: a sound rule beside an erroring one still speaks in its own voice.
//
// The erroring gate is bound to commands and stays dormant on a write; the sound
// write-gate is the only one that fires, and it must produce its OWN check's reason
// rather than the engine's diagnostic leaking onto it. Having a broken command-rule
// loaded must not poison an unrelated sound write-rule.
func TestT014_05_ASoundRuleKeepsItsOwnReason(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "npm-access", gateMatcherErrorsAtRuntime, map[string]string{
		"check.sh": permitCheck,
	})
	e.Gate(proj, "writes", gateBindEveryWrite, map[string]string{
		"check.sh": refuseCheck,
	})

	got := e.Run(proj, "s-014-05", "write a note", Turns("done",
		Write("w1", "any/notes.md", "hello"),
	))

	if got.Permitted() {
		t.Fatalf("the sound rule did not refuse:\n%s", got.Output)
	}
	if !got.Saw("the check refused") {
		t.Errorf("the sound rule's own reason was replaced:\n%s", got.Output)
	}
}

// A gate whose CHECK carries a NUL byte in its script command.
//
// `\x00` is a valid escape in a double-quoted YAML scalar, so this parses, and the
// script is not a path reference the loader judges — the gate loads SOUND, with no
// problems at all. The failure comes later, at exec: `sh -c "echo hi\x00there"`
// cannot be passed to the kernel, and `sh` never starts. This is the check analogue
// of 004's unrunnable-hook case and of the old suite's NUL-byte hook command.
//
// The Go literal carries a BACKSLASH-x-0-0 so the file on disk does, and yaml
// unescapes it into a real NUL. A literal NUL in the Go source would not compile,
// and a raw NUL in the file would make the YAML invalid — which would test the
// parser instead of this branch.
const gateCheckWithNulByte = "on:\n" +
	"  - event: PreFileWrite\n" +
	"checks:\n" +
	"  - script: \"echo hi\\x00there\"\n"

// T014_06: a check the OS refuses to launch refuses the action.
//
// The third member of the family 004 and T014_01 establish: a check that exits
// non-zero refuses, a matcher that cannot be evaluated refuses, and a check that
// cannot be STARTED must refuse too. The check runner (internal/dispatch/exec.go,
// runScriptExec) turns a start error into a refusal (fail-closed) rather than
// erroring up to a caller who would decide again.
//
// Re-vehicled from the old NUL-byte HOOK COMMAND to a NUL-byte CHECK COMMAND — the
// new format's check runner is the mechanism that must fail closed on an
// unlaunchable process. The refusal wording is the new runner's ("could not be
// run", not the old "could not run its hook"); the invariant is identical: a check
// that cannot be launched refuses, names the rule, and carries the OS's own error.
func TestT014_06_ACheckTheOSWillNotLaunchRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	// No script file: the command is not a path reference, so there is nothing to
	// put on disk and nothing for the load check to judge.
	e.Gate(proj, "unlaunchable", gateCheckWithNulByte, nil)

	got := e.Run(proj, "s-014-06", "write a note", Turns("done",
		Write("w1", "any/notes.md", "hello"),
	))

	if got.Permitted() {
		t.Fatalf("a check the OS would not launch was read as approval:\n%s", got.Output)
	}
	if !got.Saw("unlaunchable") {
		t.Errorf("the refusal does not name the gate that could not run:\n%s", got.Output)
	}
	if !got.Saw("could not be run") {
		t.Errorf("the refusal does not say the check could not be run:\n%s", got.Output)
	}
	// The OS's own message travels with it. Without it an author sees a refusal
	// naming a command that looks perfectly fine, and has nothing to go on.
	if !got.Saw("invalid argument") {
		t.Errorf("the refusal does not carry what the OS said:\n%s", got.Output)
	}
}

// A gate about a declared string field on the command kind, read the ordinary way.
// Sound, ordinary, and the one under test in T014_07 — the control that keeps
// T014_01 and T014_06 from being satisfied by an engine that refuses everything at
// this hook point.
const gateOnDeclaredString = `on:
  - event: PreCommandInvoke
    match: event.raw startsWith "npm"
checks:
  - script: ./check.sh
`

// T014_07: the engine still decides cleanly when a matcher CAN be compiled and
// answered in both directions.
//
// Deliberately paired with the wrong-typed-value unit tests in internal/guardrail
// rather than duplicating them here. A wrong-typed carried value cannot be provoked
// through the real producers — commandmod always sends `raw` as a string — so the
// e2e level can only assert that the ordinary path stays open in both directions,
// and internal/guardrail's TestMatch_WrongTypedCarriedValueErrors pins that a
// wrong-typed value errors rather than silently answering false.
//
// That split is the honest one. Reaching for a fake producer here would test a
// module this build does not have.
func TestT014_07_AStringRuleOnARealProducerStillDecides(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "raw-rule", gateOnDeclaredString, map[string]string{
		"check.sh": refuseCheck,
	})

	// Matches: refuses, through the check, in the check's own words.
	got := e.Run(proj, "s-014-07a", "publish", Turns("done",
		Bash("b1", "npm publish"),
	))
	if got.Permitted() {
		t.Fatalf("a rule reading a declared string did not fire on a matching command:\n%s", got.Output)
	}
	if !got.Saw("the check refused") {
		t.Errorf("the check's own reason did not reach the agent:\n%s", got.Output)
	}

	// Does not match: permits. A declared string carried as a string answers
	// cleanly in both directions, which is what the wrong-type error must not
	// disturb.
	got = e.Run(proj, "s-014-07b", "list", Turns("done",
		Bash("b1", "ls -la"),
	))
	if got.Refused() {
		t.Fatalf("a rule that answered 'no' on a well-typed value was treated as failing:\n%s", got.Output)
	}
}

// A gate that permits every write it is shown.
const gatePermitEveryWrite = `on:
  - event: PreFileWrite
checks:
  - script: ./check.sh
`

// T014_08: a permitted write to a path containing a refusal word is PERMITTED.
//
// This is the finding about the test helper rather than about the engine, and it
// belongs in the suite because the helper is what every other assertion here rests
// on. A predicate that substring-scanned the whole mock stream for
// "deny"/"denied"/"block"/"blocked" would answer "refused" for this exact scenario
// — a gate permitting everything and a write to `deny/notes.md` — because the
// stream carries the agent's own tool input. harness.Refused reads the harness's
// own refusal marker instead, so a permitted write to such a path reads as
// permitted.
func TestT014_08_APermittedWriteToAPathNamedDenyIsPermitted(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "open", gatePermitEveryWrite, map[string]string{
		"check.sh": permitCheck,
	})

	got := e.Run(proj, "s-014-08", "write a note", Turns("done",
		Write("w1", "deny/notes.md", "this content is about blocked requests"),
	))

	if got.Refused() {
		t.Fatalf("a permitted write was reported as refused because its path said 'deny':\n%s", got.Output)
	}
	if !got.Saw("File written successfully") {
		t.Errorf("the write did not actually go through, so this proves nothing:\n%s", got.Output)
	}
}
