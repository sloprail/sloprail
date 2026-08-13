// Package e2e covers what a project hears when one of its declarations cannot
// do what it says, at the hook point where the work is actually happening.
//
// The load check has always worked: `Validate` produces an exact diagnostic for
// a matcher naming a field its kind does not carry, and session start prints it.
// But the pre-tool path discarded the invalid list entirely, so the same load
// produced a diagnostic once at session start and silence at every write after
// it. A typo was announced when nobody was looking and then disarmed its rule
// for the rest of the session, with no trace at the moment it mattered.
//
// That is worse than the defect it replaced. A rule that never fires at least
// leaves the declaration visibly present; a rule silently dropped at every
// enforcement point is indistinguishable from a rule being satisfied.
package e2e

import (
	"strings"
	"testing"
)

// A guardrail whose matcher misspells `path`. Everything else about it is
// correct — the kind exists, the hook is present and executable, and the rule
// would refuse every write under `guarded/` if the name were spelled right.
//
// This is the shape of an ordinary typo, which is the point: nothing about it
// announces itself, and only the kind's declared fields tell it from a rule that
// legitimately does not match.
const misspelledField = `---
hooks:
  PreFileCreate:
    - matcher: paht startsWith "guarded/"
      hooks:
        - type: command
          command: ./refuse.sh
---

# Refuses writes under guarded/, except it says paht
`

// The same rule with the field spelled correctly. The control: it proves the
// declaration is otherwise sound and that the write under test is one this
// guardrail really does refuse, so a silent pass in the test above is the typo
// being swallowed rather than the rule simply not applying.
const spelledCorrectly = `---
hooks:
  PreFileCreate:
    - matcher: path startsWith "guarded/"
      hooks:
        - type: command
          command: ./refuse.sh
---

# Refuses writes under guarded/
`

// A guardrail binding an event no module in this build produces. The other way
// a declaration can be disqualified at load, and it must be as audible as the
// first — an author who mistyped a kind gets exactly the same silence otherwise.
const unknownKind = `---
hooks:
  PreFileNosuchthing:
    - hooks:
        - type: command
          command: ./refuse.sh
---

# Bound to an event that does not exist
`

// A declaration broken in the same way, but bound to file DELETION rather than
// creation. Used to show that a broken rule stops what it was about and nothing
// else — a write must survive it.
const brokenOnDelete = `---
hooks:
  PreFileDelete:
    - matcher: paht startsWith "guarded/"
      hooks:
        - type: command
          command: ./refuse.sh
---

# Refuses deletions under guarded/, except it says paht
`

const refuseScript = "#!/bin/sh\ncat >/dev/null\necho 'guarded/ is off limits' >&2\nexit 1\n"

// permitted reports whether the write went through.
func permitted(output string) bool {
	for _, sign := range []string{"deny", "denied", "block", "blocked"} {
		if strings.Contains(output, sign) {
			return false
		}
	}
	return true
}

// T013_01: a matcher naming a field its kind does not carry stops the write it
// was supposed to guard, and says why.
//
// This is the finding. `LoadWith` returns the invalid declarations and the
// pre-tool path discarded them with `_`, so the guardrail vanished and the write
// proceeded with nothing on any channel. The comment three lines above the call
// claimed the opposite.
//
// Note what this asserts, and why merely printing a diagnostic would not satisfy
// it. At PreToolUse a harness forwards a hook's stderr to the agent ONLY when
// the hook exits non-zero — verified by probing every other channel through this
// same mock: stderr at exit 0, stdout at exit 0, `systemMessage` and
// `additionalContext` are all swallowed. So "warn and proceed" is
// indistinguishable from the original silence at the only place that matters.
// The refusal is the diagnostic's delivery mechanism, not a separate decision.
func TestT013_01_MisspelledMatcherFieldStopsTheWriteItGuarded(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "typo", misspelledField, map[string]string{
		"refuse.sh": refuseScript,
	})

	got := e.Run(proj, "s-013-01", "write a note", Turns("done",
		Write("w1", "guarded/notes.md", "hello"),
	))

	if permitted(got.Output) {
		t.Fatalf("a guardrail that could not load let through the write it was written to guard:\n%s", got.Output)
	}
	if !got.Saw("paht") {
		t.Errorf("nothing named the misspelled field, so the author cannot find it:\n%s", got.Output)
	}
	if !got.Saw("typo") {
		t.Errorf("the diagnostic does not name the guardrail it came from:\n%s", got.Output)
	}
	// Validate's diagnostic lists what the kind does carry, which is the half
	// that lets an author fix it rather than merely know something is wrong.
	if !got.Saw("path (string)") {
		t.Errorf("the diagnostic does not say what the kind carries:\n%s", got.Output)
	}
	// A refusal an author cannot clear is a trap. The way out has to travel with
	// the refusal, since this fires on every action the rule was bound to.
	if !got.Saw("enabled: false") {
		t.Errorf("the refusal does not say how to get unstuck:\n%s", got.Output)
	}
}

// T013_02: the control. The same rule spelled correctly refuses the same write.
//
// Without this, T013_01 could be satisfied by an engine that refuses everything,
// or by one where the guardrail never applied to this path in the first place.
func TestT013_02_TheSameRuleSpelledRightRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "correct", spelledCorrectly, map[string]string{
		"refuse.sh": refuseScript,
	})

	got := e.Run(proj, "s-013-02", "write a note", Turns("done",
		Write("w1", "guarded/notes.md", "hello"),
	))

	if permitted(got.Output) {
		t.Fatalf("the correctly spelled rule did not refuse the write it guards:\n%s", got.Output)
	}
	if !got.Saw("guarded/ is off limits") {
		t.Errorf("the hook's own reason did not reach the agent:\n%s", got.Output)
	}
}

// T013_03: a broken rule stops the events it bound to WHATEVER the path.
//
// The typo is in a matcher narrowing to `guarded/`, but the rule cannot load, so
// nothing knows that narrowing was ever intended — the expression that expressed
// it is the broken part. The honest scope is the KIND it bound to, which is
// every file creation. Narrower would mean trusting a matcher already known not
// to compile.
func TestT013_03_ABrokenRuleStopsEveryEventOfItsKind(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "typo", misspelledField, map[string]string{
		"refuse.sh": refuseScript,
	})

	// Deliberately NOT under guarded/ — the path the broken matcher meant to
	// narrow to.
	got := e.Run(proj, "s-013-03", "write a note", Turns("done",
		Write("w1", "elsewhere/notes.md", "hello"),
	))

	if permitted(got.Output) {
		t.Fatalf("a rule that could not load was treated as narrowing to a path its broken matcher named:\n%s", got.Output)
	}
	if !got.Saw("PreFileCreate") {
		t.Errorf("the refusal does not name the event kind that is unguarded:\n%s", got.Output)
	}
}

// T013_04: a declaration binding an event no module produces refuses nothing.
//
// The other disqualifying fault, and the one where scoping does the work. The
// rule binds `PreFileNosuchthing`, which no module produces — so no event of
// that kind ever occurs, nothing is unguarded by its absence, and writes carry
// on. A fix that refused on any invalid declaration would block this write for a
// rule that was never about it.
func TestT013_04_ARuleBoundToNothingBlocksNothing(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "nokind", unknownKind, map[string]string{
		"refuse.sh": refuseScript,
	})

	got := e.Run(proj, "s-013-04", "write a note", Turns("done",
		Write("w1", "any/notes.md", "hello"),
	))

	if !permitted(got.Output) {
		t.Fatalf("a rule bound to an event that cannot occur blocked an unrelated write:\n%s", got.Output)
	}
}

// T013_05: a broken rule about one kind does not block a different kind.
//
// The scoping property, stated where it can fail. `guardrail.Fault` warns that
// dropping a rule can convert it into a fail-open; the mirror mistake is
// refusing so broadly that a typo in one rule blocks every action in the
// project, leaving no way out but deleting it. A broken rule about file DELETION
// must not stop a file being written.
func TestT013_05_ABrokenRuleDoesNotBlockAnUnrelatedKind(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "delete-typo", brokenOnDelete, map[string]string{
		"refuse.sh": refuseScript,
	})

	got := e.Run(proj, "s-013-05", "write a note", Turns("done",
		Write("w1", "any/notes.md", "hello"),
	))

	if !permitted(got.Output) {
		t.Fatalf("a broken rule about deletions blocked a write it was never about:\n%s", got.Output)
	}
}

// T013_06: one broken declaration does not silence a sound one beside it.
//
// A project usually has several rules. The sound one still refuses on its own
// terms, with its hook's own words — so the broken rule beside it neither
// disarms it nor replaces its reason.
func TestT013_06_ASoundRuleStillRefusesOnItsOwnTerms(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "delete-typo", brokenOnDelete, map[string]string{
		"refuse.sh": refuseScript,
	})
	e.Guardrail(proj, "correct", spelledCorrectly, map[string]string{
		"refuse.sh": refuseScript,
	})

	got := e.Run(proj, "s-013-06", "write a note", Turns("done",
		Write("w1", "guarded/notes.md", "hello"),
	))

	if permitted(got.Output) {
		t.Fatalf("the sound rule did not refuse while a broken one sat beside it:\n%s", got.Output)
	}
	if !got.Saw("guarded/ is off limits") {
		t.Errorf("the sound rule's own reason was replaced by the broken one's:\n%s", got.Output)
	}
}
