package e2e

import (
	"testing"
)

// This file covers a gate on a pre-file event: a failing script check blocks the
// write, a missing skill require blocks it, and a met require with passing checks
// admits it. All three drive a real Write turn against a gate bound to
// PreFileWrite, so the whole path — mock, hook, dispatch, check-runner, deny — runs
// as a user would get it.

// alwaysFailCheck refuses every write, with a reason on stdout so the refusal
// carries the gate's own words.
const alwaysFailCheck = `#!/bin/sh
cat >/dev/null
echo '{"reason":"the depth check found this write inadequate"}'
exit 1
`

// alwaysPassCheck permits every write.
const alwaysPassCheck = `#!/bin/sh
cat >/dev/null
exit 0
`

// gateWithScript binds a script check to writes under memories/, narrowed by the
// event's own path.
const gateWithScript = `on:
  - event: PreFileWrite
    match: event.path startsWith "memories/"
checks:
  - script: ./check.sh
`

// T032_01: a gate whose script check refuses BLOCKS the write, and the write does
// not land.
// sr:proves gates/refusal-stops-the-action
func TestT032_01_FailingScriptCheckBlocks(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "depth-check", gateWithScript, map[string]string{"check.sh": alwaysFailCheck})

	res := e.Run(proj, "s-032-01", "write a memory", Turns("done",
		Write("w1", "memories/note.md", "shallow"),
	))

	if !res.Refused() {
		t.Errorf("a gate whose script check failed did not block the write:\n%s", res.Output)
	}
	if e.Exists(proj, "memories/note.md") {
		t.Errorf("the write LANDED despite the gate's check refusing it")
	}
	// The gate's own words reach the agent through the refusal.
	if !res.Saw("depth check found this write inadequate") {
		t.Errorf("the gate's refusal reason did not reach the agent:\n%s", res.Output)
	}
}

// T032_02: a gate whose script check passes ADMITS the write, and it lands.
//
// The control for T032_01: without it a gate that blocked everything would pass
// T032_01 while being broken.
// sr:proves gates/refusal-stops-the-action
func TestT032_02_PassingScriptCheckAdmits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "depth-check", gateWithScript, map[string]string{"check.sh": alwaysPassCheck})

	res := e.Run(proj, "s-032-02", "write a memory", Turns("done",
		Write("w1", "memories/note.md", "deep enough"),
	))

	if res.Refused() {
		t.Errorf("a gate whose script check passed refused the write anyway:\n%s", res.Output)
	}
	if !e.Exists(proj, "memories/note.md") {
		t.Errorf("the write did not land though the gate admitted it")
	}
}

// gateRequireSkill is a PURE-require gate: writing under memories/topics/ is
// blocked until the document-topic skill was loaded this session. No checks —
// require is the whole rule, the exact shape
// sloprail-community's examples/required-context-precondition ships.
const gateRequireSkill = `on:
  - event: PreFileWrite
    match: event.path startsWith "memories/topics/"
require:
  - skill: document-topic
`

// T032_03: a gate with an unmet skill require BLOCKS the write.
//
// The write happens with no Skill turn before it, so the record holds no Skill
// tool_use for document-topic and the require fails — the native skill check, the
// thing that absorbs ~100 lines of trajectory plumbing.
func TestT032_03_MissingSkillRequireBlocks(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "require-topic", gateRequireSkill, nil)

	res := e.Run(proj, "s-032-03", "write a topic without loading the skill", Turns("done",
		Write("w1", "memories/topics/idea.md", "# an idea"),
	))

	if !res.Refused() {
		t.Errorf("a gate requiring an unloaded skill did not block the write:\n%s", res.Output)
	}
	if e.Exists(proj, "memories/topics/idea.md") {
		t.Errorf("the write LANDED despite the skill require being unmet")
	}
	if !res.Saw("document-topic") {
		t.Errorf("the refusal did not name the required skill:\n%s", res.Output)
	}
}

// T032_04: the SAME gate ADMITS once the skill was loaded earlier in the session.
//
// A Skill turn precedes the write, so a real Skill tool_use for document-topic is
// in the record and the native check finds it. This is the positive half of the
// skill require, and the control that proves T032_03 is not blocking for some
// unrelated reason.
func TestT032_04_LoadedSkillRequireAdmits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "require-topic", gateRequireSkill, nil)

	res := e.Run(proj, "s-032-04", "load the skill then write a topic", Turns("done",
		Skill("s1", "document-topic"),
		Write("w1", "memories/topics/idea.md", "# an idea"),
	))

	if res.Refused() {
		t.Errorf("a gate whose skill require was met refused the write anyway:\n%s", res.Output)
	}
	if !e.Exists(proj, "memories/topics/idea.md") {
		t.Errorf("the write did not land though the skill was loaded")
	}
}

// T032_05: a gate only fires on the events its trigger matches — a write OUTSIDE
// the narrowed path is untouched.
//
// The control for the trigger's `match`. Without it, T032_01/03 would pass against
// an engine that ran the gate on every write and ignored the path narrowing.
func TestT032_05_GateDoesNotFireOutsideMatch(t *testing.T) {
	e := New(t)
	proj := e.Project()
	// A gate that would refuse everything it fires on, narrowed to memories/.
	e.Gate(proj, "depth-check", gateWithScript, map[string]string{"check.sh": alwaysFailCheck})

	// A write OUTSIDE memories/ — the gate's match does not admit it, so the gate
	// does not fire and the write is permitted.
	res := e.Run(proj, "s-032-05", "write outside the gated area", Turns("done",
		Write("w1", "docs/readme.md", "hello"),
	))

	if res.Refused() {
		t.Errorf("a gate narrowed to memories/ fired on a write to docs/:\n%s", res.Output)
	}
	if !e.Exists(proj, "docs/readme.md") {
		t.Errorf("a write the gate should not have touched did not land")
	}
}
