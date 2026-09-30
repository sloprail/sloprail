// Package e2e covers what a project's protection does when one of its
// declarations cannot do what it says, at the pre-tool point where the work is
// actually happening: a broken declaration must block NOTHING (it cannot be trusted
// to have narrowed anything, so it must not refuse), must not disarm a sound rule
// beside it, and is REPORTED rather than silently swallowed.
//
// The new dispatch loads new-format declarations through a store that separates the
// ones that loaded from the ones that could not (internal/declaration/store.go: a
// malformed gate.yaml becomes an entry in loaded.Invalid with its reasons,
// never a loaded gate). At every pre-tool dispatch newNatureDeclarations reports
// that invalid set on stderr via reportNatureInvalid, and the invalid gates
// dispatch NOTHING — the same "an invalid guardrail blocks nothing, but is named"
// stance the old format settled on. These tests install malformed gate.yaml
// files and re-prove the OBSERVABLE half against the new dispatch.
//
// # What is and is not observable here, precisely
//
// "Blocks nothing" and "does not disarm a sound neighbour" ARE observable — they
// are refusals that do or do not arrive, read with res.Permitted / res.Refused /
// res.Saw. The stderr REPORT (reportNatureInvalid's "declaration <x> not loaded")
// is NOT observable through this mock: measured in this repo, a PERMITTED action
// carries neither stdout nor stderr back to the agent, and a broken rule is exactly
// the permitted case. That is why the report's own delivery is pinned one layer
// down — the store's Invalid production is asserted in internal/declaration
// (store_test.go loadOneInvalid), and the stderr report itself is pinned in
// services/sr-session (nature_reportinvalid_test.go, whose tests drive this same
// dispatch with stderr captured and assert both "not loaded, naming the guard" and
// "does not deny"). A test that looks like it pins the report and cannot is worse
// than one that says plainly where the report is pinned; this file asserts the
// agent-visible consequence and leaves the stderr line to the layer that can read
// it.
package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// badMatchGuard is a gate whose trigger match names a field the event scope does not
// carry — the bare `marker` (the scope exposes `event.newMarkers`). This is the
// shape of an ordinary typo: nothing about it announces itself, and only the
// scope's declared fields tell it from a rule that legitimately does not match. It
// would be a gate refusing every write if the match compiled.
const badMatchGuard = `on:
  - event: PreFileWrite
    match: marker.kind == "endpoint"
checks:
  - script: ./refuse.sh
`

// soundGuard is a correct gate that refuses every markdown write —
// the control proving a write under test is one a sound rule really does refuse.
const soundGuard = `on:
  - event: PreFileWrite
    match: event.path endsWith ".md"
checks:
  - script: ./refuse.sh
`

// missingMatchGuard omits `on` entirely — the other way a gate is
// disqualified at load (a required field absent, ErrMissingField). As audible as
// the first, and it must block nothing.
const missingMatchGuard = `checks:
  - script: ./refuse.sh
`

// unparseableGuard is not a rule with a mistake in it but a file the YAML parser
// cannot get a declaration out of at all.
const unparseableGuard = `:this is not: valid yaml: at all
  - [unbalanced
`

const refuseScript = "#!/bin/sh\ncat >/dev/null\necho '{\"reason\":\"guarded is off limits\"}'\nexit 1\n"

// T013_01: a match naming a field its scope does not carry does NOT stop the write
// it was supposed to guard.
//
// An invalid guardrail blocks nothing. The fault belongs to the guardrail author
// and is reported where the author looks (session start and stderr), not to the
// agent, which did not write the file and often cannot repair it.
func TestT013_01_MalformedMatchDoesNotStopTheWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "typo", badMatchGuard, map[string]string{"refuse.sh": refuseScript})

	got := e.Run(proj, "s-013-01", "write a note", Turns("done",
		Write("w1", "guarded/notes.md", "hello"),
	))

	if !got.Permitted() {
		t.Fatalf("a gate that could not load must not refuse the write:\n%s", got.Output)
	}
}

// T013_02: the control. The same rule spelled correctly refuses the same write.
//
// Without this, T013_01 could be satisfied by an engine where the guardrail never
// applied to this path in the first place.
func TestT013_02_TheSameRuleSpelledRightRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "correct", soundGuard, map[string]string{"refuse.sh": refuseScript})

	got := e.Run(proj, "s-013-02", "write a note", Turns("done",
		Write("w1", "guarded/notes.md", "hello"),
	))

	if got.Permitted() {
		t.Fatalf("the correctly spelled rule did not refuse the write it guards:\n%s", got.Output)
	}
	if !got.Saw("guarded is off limits") {
		t.Errorf("the check's own reason did not reach the agent:\n%s", got.Output)
	}
}

// T013_03: a broken rule stops nothing, on any path.
//
// A rule that cannot load refuses neither the path its match named nor any other.
func TestT013_03_ABrokenRuleStopsNothing(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "typo", badMatchGuard, map[string]string{"refuse.sh": refuseScript})

	got := e.Run(proj, "s-013-03", "write a note", Turns("done",
		Write("w1", "elsewhere/notes.md", "hello"),
	))

	if !got.Permitted() {
		t.Fatalf("a rule that could not load must not refuse anything:\n%s", got.Output)
	}
}

// T013_04: a gate missing its required `on` refuses nothing.
//
// The other disqualifying fault (ErrMissingField). A fix that refused on any
// invalid declaration would block this write for a rule that could not say what it
// was about.
func TestT013_04_ARuleMissingItsMatchBlocksNothing(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "nomatch", missingMatchGuard, map[string]string{"refuse.sh": refuseScript})

	got := e.Run(proj, "s-013-04", "write a note", Turns("done",
		Write("w1", "any/notes.md", "hello"),
	))

	if got.Refused() {
		t.Fatalf("a gate with no trigger blocked an unrelated write:\n%s", got.Output)
	}
}

// T013_06: one broken declaration does not silence a sound one beside it.
//
// A project usually has several rules. The sound one still refuses on its own
// terms, with its check's own words — so the broken rule beside it neither disarms
// it nor replaces its reason. This is the "not silently ignored" invariant at its
// sharpest: a broken declaration that silently disarmed its neighbours would leave
// the project looking guarded while it was not.
func TestT013_06_ASoundRuleStillRefusesOnItsOwnTerms(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "typo", badMatchGuard, map[string]string{"refuse.sh": refuseScript})
	e.Gate(proj, "correct", soundGuard, map[string]string{"refuse.sh": refuseScript})

	got := e.Run(proj, "s-013-06", "write a note", Turns("done",
		Write("w1", "guarded/notes.md", "hello"),
	))

	if got.Permitted() {
		t.Fatalf("the sound rule did not refuse while a broken one sat beside it:\n%s", got.Output)
	}
	if !got.Saw("guarded is off limits") {
		t.Errorf("the sound rule's own reason was replaced by the broken one's:\n%s", got.Output)
	}
}

// T013_07: a declaration that cannot be parsed at all permits, and goes quiet.
//
// An unparseable gate.yaml names no bindings, so it cannot scope a refusal —
// and the engine does NOT respond by refusing everything (the old fail-closed this
// change removed). The project is unguarded by that rule and quiet about it at this
// point; the mitigation lives at session start and on stderr, where the author who
// broke the file is looking.
func TestT013_07_AnUnparseableDeclarationPermits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "unreadable", unparseableGuard, nil)

	got := e.Run(proj, "s-013-07", "write a note", Turns("done",
		Write("w1", "any/notes.md", "hello"),
	))

	if !got.Permitted() {
		t.Fatalf("a declaration too broken to read must not refuse the action:\n%s", got.Output)
	}
}

// T013_08: an unreadable declaration does not lock the session out of REPAIRING it.
//
// The write that repairs the declaration is itself a write; a blanket refusal on a
// broken file would refuse that write too, and every unrelated command besides,
// leaving no way out but deleting the folder by hand. Under "an invalid guardrail
// blocks nothing" both remedies are trivially permitted — the property is
// structural rather than argued.
func TestT013_08_TheRemedyIsNotRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "unreadable", unparseableGuard, nil)

	// Remedy one: overwrite the broken declaration with a valid one. Reads the
	// skill and its gate.md page first — the shipped read-gate-doc
	// gate requires it for any write under .sloprail/gate/*/gate.yaml,
	// the same precondition a real repair now meets.
	got := e.Run(proj, "s-013-08-fix", "fix the declaration", Turns("done",
		Skill("s1", "authoring-guardrails"),
		ToolUse("r1", "Read", map[string]string{"file_path": harness.ShippedSkillFile(t, "gate.md")}),
		Write("w1", ".sloprail/gate/unreadable/gate.yaml", soundGuard),
	))
	if !got.Permitted() {
		t.Fatalf("the write that REPAIRS the broken declaration was refused, so the fix could not be carried out:\n%s", got.Output)
	}

	// Remedy two: an unrelated command, which a blanket refusal would also stop.
	got = e.Run(proj, "s-013-08-cmd", "list the files", Turns("done",
		Bash("b1", "ls -la"),
	))
	if !got.Permitted() {
		t.Fatalf("an unrelated command was refused by a declaration that guards nothing:\n%s", got.Output)
	}
}

// T013_09: a project with no broken declarations is unaffected by any of this.
//
// The guard against the "blocks nothing" leaking into "permits everything": a
// project whose declarations all parse must still enforce them.
func TestT013_09_ASoundProjectStillEnforces(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "correct", soundGuard, map[string]string{"refuse.sh": refuseScript})

	// Under guarded — the sound rule refuses (its match is every .md).
	got := e.Run(proj, "s-013-09-refuse", "write a note", Turns("done",
		Write("w1", "guarded/notes.md", "hello"),
	))
	if got.Permitted() {
		t.Fatalf("a sound project stopped enforcing:\n%s", got.Output)
	}
}
