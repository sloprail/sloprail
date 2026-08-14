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

// Whether the write went through is asked of the Result — see harness.Refused.
// A local copy of that predicate lived here and in 014, and both scanned the
// whole stream for "deny"/"denied"/"block"/"blocked". The stream carries the
// agent's own tool input, so a fully PERMITTED write to `deny/notes.md` was
// reported as refused, and every `!permitted(...)` assertion below would have
// passed on a permitted write the moment a fixture used such a path. One
// definition, in the harness, keyed on the harness's own refusal marker.

// T013_01: a matcher naming a field its kind does not carry does NOT stop the
// write it was supposed to guard.
//
// This test formerly asserted the reverse, and the reversal is the whole of the
// change it now pins: an invalid guardrail blocks nothing.
//
// The engine's position used to be that refusing IS the diagnostic's delivery
// mechanism, since no channel out of a PreToolUse hook reaches the agent without
// also refusing — measured through this same mock, one channel per run: stdout
// and stderr at exit 0, stderr at exits 1, 3, 126 and 127, `systemMessage` and
// `additionalContext` are all swallowed; only stderr at exit 2 and the JSON
// `permissionDecision: "deny"` arrive, and both refuse.
//
// That table is still accurate, so this change really does trade the agent's
// awareness away. What it buys is that a typo in somebody's declaration no
// longer halts a session that had nothing to do with it. The fault belongs to
// the guardrail author and is reported where the author looks — session start
// and stderr — rather than to the agent, which did not write the file and often
// cannot repair it.
func TestT013_01_MisspelledMatcherFieldDoesNotStopTheWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "typo", misspelledField, map[string]string{
		"refuse.sh": refuseScript,
	})

	got := e.Run(proj, "s-013-01", "write a note", Turns("done",
		Write("w1", "guarded/notes.md", "hello"),
	))

	if !got.Permitted() {
		t.Fatalf("a guardrail that could not load must not refuse the write:\n%s", got.Output)
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

	if got.Permitted() {
		t.Fatalf("the correctly spelled rule did not refuse the write it guards:\n%s", got.Output)
	}
	if !got.Saw("guarded/ is off limits") {
		t.Errorf("the hook's own reason did not reach the agent:\n%s", got.Output)
	}
}

// T013_03: a broken rule stops nothing, on any path.
//
// The old rule scoped the refusal to the KIND the declaration bound to, since a
// matcher that will not compile cannot be trusted to have narrowed anything. The
// scoping reasoning was sound; what it scoped is now gone. A rule that cannot
// load refuses neither the path its matcher named nor any other.
func TestT013_03_ABrokenRuleStopsNothing(t *testing.T) {
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

	if !got.Permitted() {
		t.Fatalf("a rule that could not load must not refuse anything:\n%s", got.Output)
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

	if got.Refused() {
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

	if got.Refused() {
		t.Fatalf("a broken rule about deletions blocked a write it was never about:\n%s", got.Output)
	}
}

// T013_05b is DELETED, and this note records why rather than leaving a silent
// gap between 05 and 06.
//
// It was the positive control for T013_05: the same fault moved onto the kind
// the write actually produces, proving the permit in T013_05 came from
// kind-scoping rather than from broken declarations having stopped mattering
// altogether. Its own comment made the standard explicit — "a test whose subject
// can be deleted while it still passes is not testing its subject".
//
// Broken declarations HAVE now stopped mattering altogether, deliberately, so
// the control can no longer distinguish anything: its assertion was that a
// broken rule on the write's own kind refuses the write, which is exactly what
// this change removes. Keeping it inverted would duplicate T013_01, which
// already pins that a broken rule on PreFileCreate permits a creation.
//
// What this costs is worth naming: T013_05 is now a test that only asserts
// something did NOT happen, in a build where nothing invalid ever refuses, so it
// no longer locates a boundary. It is kept as a regression guard against a
// future build that reintroduces refusal without reintroducing scoping.

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

	if got.Permitted() {
		t.Fatalf("the sound rule did not refuse while a broken one sat beside it:\n%s", got.Output)
	}
	if !got.Saw("guarded/ is off limits") {
		t.Errorf("the sound rule's own reason was replaced by the broken one's:\n%s", got.Output)
	}
}

// A declaration with no frontmatter fence at all — not a rule with a mistake in
// it, but a file the parser cannot get a declaration out of. `loadOne` returns a
// zero Declaration and one malformed problem carrying no event, so it binds
// nothing and names no kinds.
const unparseable = `this file has no frontmatter fence at all

# Whatever this was meant to be
`

// The same fault by the other route: the fence opens and never closes, so the
// frontmatter is unterminated. Included so T013_07 is about "cannot be parsed"
// rather than about one spelling of it.
const unterminatedFrontmatter = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./refuse.sh

# The closing fence is missing
`

// T013_07: a declaration that cannot be parsed at all permits, and goes quiet.
//
// The inversion of what this asserted, and the case that forced the change. An
// unparseable declaration names no bindings, so the old engine could not scope a
// refusal and refused EVERYTHING instead — every kind, every action, for the
// life of the session.
//
// The cost of that is not theoretical and was not what the old comment predicted.
// See T013_08, which used to claim the author kept a way out and has been
// rewritten to measure whether that was ever true.
//
// What is knowingly given up: at this hook point the agent is not told. The
// project is unguarded and quiet about it, and the mitigation lives at session
// start, where the author who broke the file is looking.
func TestT013_07_AnUnparseableDeclarationPermits(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"no frontmatter fence", unparseable},
		{"unterminated frontmatter", unterminatedFrontmatter},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New(t)
			proj := e.Project()
			e.Guardrail(proj, "unreadable", tc.body, nil)

			got := e.Run(proj, "s-013-07-"+strings.ReplaceAll(tc.name, " ", "-"),
				"write a note", Turns("done",
					Write("w1", "any/notes.md", "hello"),
				))

			if !got.Permitted() {
				t.Fatalf("a declaration too broken to read must not refuse the action:\n%s", got.Output)
			}
		})
	}
}

// T013_08: an unreadable declaration does not lock the session out of REPAIRING
// it — and this version actually measures that, where the old one asserted it.
//
// The claim it replaces was: "the refusal does not lock the project out of
// fixing it… editing the broken GUARDRAIL.md is itself a write, and a write is
// refused by a rule that is bound to it — which this one, being unreadable, is
// not bound to anything." Every clause of that is true about BINDINGS and the
// conclusion is still false, because the blanket refusal did not run through
// bindings at all: it fired before extraction, on every action of every kind.
//
// So the write that repairs the declaration was refused by the declaration's own
// brokenness, and so was deleting the folder, and so was every unrelated command.
// The old T013_08 did not catch this despite being the test named for it — it
// only checked that the refusal EXPLAINED itself, never that a remedy could be
// carried out.
//
// This drives the two remedies the refusal text itself named. Both must be
// permitted, which under "an invalid guardrail blocks nothing" they trivially
// are — and that is the point: the property is now structural rather than
// argued.
func TestT013_08_TheRemedyIsNotRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "unreadable", unparseable, nil)

	// Remedy one: overwrite the broken declaration with a valid one. This is the
	// action the old refusal text asked for and simultaneously refused.
	got := e.Run(proj, "s-013-08-fix", "fix the declaration", Turns("done",
		Write("w1", ".sloprail/guardrails/unreadable/GUARDRAIL.md", spelledCorrectly),
	))
	if !got.Permitted() {
		t.Fatalf("the write that REPAIRS the broken declaration was refused, "+
			"so the refusal's own instruction could not be carried out:\n%s", got.Output)
	}

	// Remedy two: an unrelated command, which the blanket refusal also stopped.
	got = e.Run(proj, "s-013-08-cmd", "list the files", Turns("done",
		Bash("b1", "ls -la"),
	))
	if !got.Permitted() {
		t.Fatalf("an unrelated command was refused by a declaration that guards nothing:\n%s", got.Output)
	}
}

// T013_09: a project with no broken declarations is unaffected by any of this.
//
// The guard against the blanket refusal leaking. T013_07 refuses everything on
// an unreadable file; a project whose declarations all parse must still run
// normally, or the fix has replaced one fail-closed for every project.
func TestT013_09_ASoundProjectIsNotRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "correct", spelledCorrectly, map[string]string{
		"refuse.sh": refuseScript,
	})

	// Not under guarded/, so the sound rule declines and nothing else has an
	// opinion.
	got := e.Run(proj, "s-013-09", "write a note", Turns("done",
		Write("w1", "elsewhere/notes.md", "hello"),
	))

	if got.Refused() {
		t.Fatalf("a project whose declarations all parse was refused:\n%s", got.Output)
	}
}
