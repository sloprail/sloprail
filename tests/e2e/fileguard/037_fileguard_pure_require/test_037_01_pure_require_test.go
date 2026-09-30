package e2e

import (
	"strings"
	"testing"
)

// pureRequireGate is a PURE-require GATE: a write under memories/topics/ is
// permitted only once the document-topic skill was loaded this session. There are
// NO checks — the `require:` is the whole rule. A gate, so the precondition is
// enforced at pre-tool, before the write lands, which is what lets the enforcement
// tests assert the write did not reach disk.
const pureRequireGate = `on:
  - event: PreFileWrite
    match: event.path startsWith "memories/topics/" and event.path endsWith ".md"
require:
  - skill: document-topic
`

// pureRequireGuard is the plain file-guard of the same rule: it judges the settled
// file at Stop with the same `require:` and no checks.
const pureRequireGuard = `match: "memories/topics/**/*.md"
require:
  - skill: document-topic
`

// T037_01: a pure-require file-guard and gate (no checks) VALIDATE.
//
// The half of the change that lives at load time: `sr-file declarations` loads the
// project's `.sloprail` and reports what is in force. Before the change this guard
// was refused with a missing-field fault on `checks`; now it loads, because a
// file-guard carrying a non-empty `require:` no longer needs a check. Asserted the
// way 030 asserts a sound tree — exit 0 and the guard named in the loaded report —
// and, so the test cannot pass vacuously against the old behaviour, that the report
// does NOT carry a checks fault for the guard.
func TestT037_01_PureRequireFileGuardValidates(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "require-topic", pureRequireGuard, nil)
	e.Gate(proj, "require-topic", pureRequireGate, nil)

	res := e.CLIDirect(proj, "sr-file", "declarations", proj)
	if res.Code != 0 {
		t.Fatalf("a pure-require file-guard (no checks) must load clean, got exit %d:\n%s", res.Code, res.Output)
	}
	if !strings.Contains(res.Output, "file-guards (1): require-topic") {
		t.Errorf("the loaded report did not name the pure-require file-guard:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "gates (1): require-topic") {
		t.Errorf("the loaded report did not name the pure-require gate:\n%s", res.Output)
	}
	// Non-vacuity: the OLD engine refused this guard with a "checks" missing-field
	// fault. Prove that fault is gone, not merely that some line mentions the name.
	if strings.Contains(res.Output, "could not be loaded") {
		t.Errorf("a pure-require file-guard was reported invalid — it must load:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "checks") {
		t.Errorf("the report still complains about checks on a guard that needs none:\n%s", res.Output)
	}
}

// T037_02: the same pure-require gate ENFORCES — an unmet skill require REFUSES
// the write, and it does not land.
//
// The write happens with no Skill turn before it, so the record holds no Skill
// tool_use for document-topic and the require fails. Being a gate, it
// denies at pre-tool, so the write never reaches disk — proving the require is
// evaluated and enforced even though the guard carries no check at all.
func TestT037_02_UnmetSkillRequireRefusesWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "require-topic", pureRequireGate, nil)
	e.CommitAll(proj, "the guards")

	res := e.Run(proj, "s-037-02", "write a topic without loading the skill", Turns("done",
		Write("w1", "memories/topics/idea.md", "# an idea"),
	))

	if !res.Refused() {
		t.Fatalf("a file-guard requiring an unloaded skill did not block the write:\n%s", res.Output)
	}
	if e.Exists(proj, "memories/topics/idea.md") {
		t.Errorf("the write LANDED despite the skill require being unmet")
	}
	if !res.Saw("document-topic") {
		t.Errorf("the refusal did not name the required skill:\n%s", res.Output)
	}
}

// T037_03: the SAME gate PERMITS the write once the skill was loaded earlier in
// the session — the write lands.
//
// A Skill turn precedes the write, so a real Skill tool_use for document-topic is
// in the record and the native check finds it; the require is met, and with no
// checks to run the guard permits the write. This is the positive half of the
// pure-require enforcement and the control that proves T037_02 blocks for the
// require and not for some unrelated reason.
func TestT037_03_LoadedSkillRequirePermitsWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "require-topic", pureRequireGate, nil)
	e.CommitAll(proj, "the guards")

	res := e.Run(proj, "s-037-03", "load the skill then write a topic", Turns("done",
		Skill("s1", "document-topic"),
		Write("w1", "memories/topics/idea.md", "# an idea"),
	))

	if res.Refused() {
		t.Fatalf("a file-guard whose skill require was met refused the write anyway:\n%s", res.Output)
	}
	if !e.Exists(proj, "memories/topics/idea.md") {
		t.Errorf("the write did not land though the skill was loaded")
	}
}
