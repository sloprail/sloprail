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
// commandmod declares `invocations` as TypeList with a nil Elem
// (internal/commandmod/module.go), so this is not a contrived case — it is the
// one field shape in the shipped build where an authored rule can still error.
//
// # The half of that gap this suite does NOT close
//
// An unchecked predicate body does not merely permit errors; it permits SILENCE,
// and the two split on which operator the typo lands under:
//
//	any(invocations, len(.flags.access) > 0)   // accessor: errors → refused
//	any(invocations, .bni == "npm")            // bare ==: compiles, loads,
//	                                           // admitted=false, err=nil
//
// The first is what T014_01 covers. The second is the silent never-fires that
// CompileMatcherFor exists to prevent, fully present one level down: `.bni` is a
// typo for `.bin`, nothing rejects it at load because there is no element shape
// to check it against, and at run time it reads nil, compares unequal, and the
// rule quietly does not fire. An author gets no error and no refusal — exactly
// the failure mode a misspelled TOP-LEVEL field is caught for.
//
// This suite cannot close that from here, and neither can the engine: the fix is
// for commandmod to declare its Elem, at which point the existing load check
// catches `.bni` the same way it catches `paht`. The element's fields are
// already known — Bin string, Argv list of string, Flags map — so declaring them
// is a statement of fact rather than a new decision. That is
// internal/commandmod's to make, and is reported rather than reached into.
//
// Recorded here because the gap is otherwise invisible: every test below passes
// with it wide open.
package e2e

import "testing"

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

// Whether the action went through is asked of the Result — see harness.Refused.
// The word-scanning copy that lived here, and its twin in 013, reported a
// permitted write to a path containing "deny" as refused.

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

	if got.Permitted() {
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

	if got.Permitted() {
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

	if got.Refused() {
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

	if got.Refused() {
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

	if got.Permitted() {
		t.Fatalf("the sound rule did not refuse:\n%s", got.Output)
	}
	if !got.Saw("the hook refused") {
		t.Errorf("the sound rule's own reason was replaced:\n%s", got.Output)
	}
}

// A declaration whose command carries a NUL byte.
//
// `\x00` is a valid escape in a double-quoted YAML scalar, so this parses, and
// `checkExecutable` does not judge it because `echo` is not a path reference —
// the declaration loads SOUND, with no problems at all. The failure comes later,
// at exec: the argument cannot be passed to the kernel, and `sh` never starts.
//
// The Go literal below has to carry a BACKSLASH-x-0-0 so the file on disk does,
// and yaml unescapes it into a real NUL. A literal NUL in the Go source would
// not compile, and a raw NUL written into the file would make the YAML invalid —
// which would test the parser instead of this branch.
const commandWithNulByte = "---\n" +
	"hooks:\n" +
	"  PreFileCreate:\n" +
	"    - hooks:\n" +
	"        - type: command\n" +
	"          command: \"echo hi\\x00there\"\n" +
	"---\n" +
	"\n" +
	"# A command the OS will not launch\n"

// T014_06: a hook the OS refuses to launch refuses the action.
//
// The third member of the family 004 and T014_01 establish: a hook that exits
// non-zero refuses, a matcher that cannot be evaluated refuses, and a hook that
// cannot be STARTED must refuse too. `runHooks` returns an error for it and the
// caller turns that into a refusal.
//
// This test exists because the comment on that branch asserted it was
// unreachable — "no e2e reaches it, and `sh -c` starts even when the command
// inside it does not". That reasoning covers the command inside the shell
// failing and misses the exec of the shell ITSELF failing, which is what a NUL
// byte in the argument causes: `fork/exec /bin/sh: invalid argument`, an
// *fs.PathError rather than an *exec.ExitError.
//
// The claim was load-bearing in the worst way. Mutating that branch to PERMIT
// instead of refuse survived the entire suite, so the one branch excused from
// coverage on the strength of a false claim was the one branch with none.
func TestT014_06_AHookTheOSWillNotLaunchRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	// No scripts: the command is not a path reference, so there is nothing to
	// put on disk and nothing for the load check to judge.
	e.Guardrail(proj, "unlaunchable", commandWithNulByte, nil)

	got := e.Run(proj, "s-014-06", "write a note", Turns("done",
		Write("w1", "any/notes.md", "hello"),
	))

	if got.Permitted() {
		t.Fatalf("a hook the OS would not launch was read as approval:\n%s", got.Output)
	}
	if !got.Saw("unlaunchable") {
		t.Errorf("the refusal does not name the guardrail that could not run:\n%s", got.Output)
	}
	if !got.Saw("could not run its hook") {
		t.Errorf("the refusal does not say the hook could not be run:\n%s", got.Output)
	}
	// The OS's own message travels with it. Without it an author sees a refusal
	// naming a command that looks perfectly fine, and has nothing to go on.
	if !got.Saw("invalid argument") {
		t.Errorf("the refusal does not carry what the OS said:\n%s", got.Output)
	}
}

// A rule about a declared string field, on a kind whose producer sends it as a
// string on every ordinary path. Sound, ordinary, and the one under test in
// T014_07 — where the matcher is fine and the VALUE is not.
const ruleOnADeclaredString = `---
hooks:
  PreCommandInvoke:
    - matcher: raw startsWith "npm"
      hooks:
        - type: command
          command: ./refuse.sh
---

# Refuses npm invocations, by reading raw
`

// T014_07: the engine still refuses when a matcher CAN be compiled and the rule
// declines — the control that keeps T014_01 and T014_06 from being satisfied by
// an engine that refuses everything at this hook point.
//
// Deliberately paired with the wrong-typed-value unit tests in
// internal/guardrail rather than duplicating them here. A wrong-typed carried
// value cannot be provoked through the real producers — commandmod always sends
// `raw` as a string — so the e2e level can only assert that the ordinary path
// stays open, and internal/guardrail's TestMatch_WrongTypedCarriedValueErrors
// pins that a wrong-typed value errors rather than silently answering false.
//
// That split is the honest one. Reaching for a fake producer here would test a
// module this build does not have.
func TestT014_07_AStringRuleOnARealProducerStillDecides(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "raw-rule", ruleOnADeclaredString, map[string]string{
		"refuse.sh": refuseScript,
	})

	// Matches: refuses, through the hook, in the hook's own words.
	got := e.Run(proj, "s-014-07a", "publish", Turns("done",
		Bash("b1", "npm publish"),
	))
	if got.Permitted() {
		t.Fatalf("a rule reading a declared string did not fire on a matching command:\n%s", got.Output)
	}
	if !got.Saw("the hook refused") {
		t.Errorf("the hook's own reason did not reach the agent:\n%s", got.Output)
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

// A guardrail that permits everything it is shown.
const permitEveryWrite = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./permit.sh
---

# Permits whatever it is shown
`

// T014_08: a permitted write to a path containing a refusal word is PERMITTED.
//
// This is the finding about the test helper rather than about the engine, and it
// belongs in the suite because the helper is what every other assertion here
// rests on. The predicate that shipped in this file and in 013 substring-scanned
// the whole mock stream for "deny"/"denied"/"block"/"blocked" — and the stream
// carries the agent's own tool input, so this exact scenario, a guardrail
// permitting everything and a write to `deny/notes.md`, produced "File written
// successfully" and a helper that answered "refused".
//
// Every `!permitted(...)` assertion in both files would therefore have passed on
// a fully permitted write as soon as a fixture used such a path. They were
// non-vacuous only by the accident of clean paths.
func TestT014_08_APermittedWriteToAPathNamedDenyIsPermitted(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "open", permitEveryWrite, map[string]string{
		"permit.sh": permitScript,
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
