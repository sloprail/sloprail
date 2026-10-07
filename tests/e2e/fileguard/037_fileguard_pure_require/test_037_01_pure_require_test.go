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

// pureRequireGuard is the plain file-guard of the same rule with the same skill
// `require:` and no checks. A file-guard cannot see the session, so this is REFUSED
// at load (T037_01); the gate above is where the requirement lives.
const pureRequireGuard = `match: "memories/topics/**/*.md"
require:
  - skill: document-topic
`

// pureCitationGuard is a pure-require file-guard that is legal: a citation is read
// from the commits' trailers, which the committed range carries.
const pureCitationGuard = `match: "memories/topics/**/*.md"
require:
  - citation: {source_types: [user]}
`

// T037_01: a pure-citation file-guard and a pure-skill gate (no checks) VALIDATE;
// a file-guard carrying a skill require is REFUSED at load, naming the gate.
//
// `sr-file declarations` loads the project's `.sloprail` and reports what is in
// force. A file-guard with a non-empty `require: citation` needs no check. A file-guard
// cannot see the session, so `require: skill` on one is a load fault whose message
// carries the gate to write instead (#162).
func TestT037_01_PureRequireFileGuardValidates(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "cite-topic", pureCitationGuard, nil)
	e.Gate(proj, "require-topic", pureRequireGate, nil)

	res := e.CLIDirect(proj, "sr-file", "declarations", proj)
	if res.Code != 0 {
		t.Fatalf("a pure-citation file-guard (no checks) must load clean, got exit %d:\n%s", res.Code, res.Output)
	}
	if !strings.Contains(res.Output, "file-guards (1): cite-topic") {
		t.Errorf("the loaded report did not name the pure-require file-guard:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "gates (1): require-topic") {
		t.Errorf("the loaded report did not name the pure-require gate:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "could not be loaded") {
		t.Errorf("a pure-require declaration was reported invalid — it must load:\n%s", res.Output)
	}
}

func TestT037_01b_SkillRequireOnAFileGuardIsRefusedNamingTheGate(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "require-topic", pureRequireGuard, nil)

	res := e.CLIDirect(proj, "sr-file", "declarations", proj)
	if res.Code != 1 {
		t.Fatalf("a file-guard requiring a skill must fail to load, got exit %d:\n%s", res.Code, res.Output)
	}
	for _, want := range []string{"gate/<name>/gate.yaml", "event: PreFileWrite", "skill: document-topic"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("the load fault does not carry %q:\n%s", want, res.Output)
		}
	}
}

// T037_02: the same pure-require gate ENFORCES — an unmet skill require REFUSES
// the write, and it does not land.
//
// The write happens with no Skill turn before it, so the record holds no Skill
// tool_use for document-topic and the require fails. Being a gate, it
// denies at pre-tool, so the write never reaches disk — proving the require is
// evaluated and enforced even though the guard carries no check at all.
// sr:proves checks/skill-requirement-reads-the-record
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
// sr:proves checks/skill-requirement-reads-the-record
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
