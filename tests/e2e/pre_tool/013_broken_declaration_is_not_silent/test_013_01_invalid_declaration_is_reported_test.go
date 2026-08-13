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

// T013_01: a matcher naming a field its kind does not carry stops the write it
// was supposed to guard, and says why.
//
// This is the finding. `LoadWith` returns the invalid declarations and the
// pre-tool path discarded them with `_`, so the guardrail vanished and the write
// proceeded with nothing on any channel. The comment three lines above the call
// claimed the opposite.
//
// Note what this asserts, and why merely printing a diagnostic would not satisfy
// it. No channel out of a PreToolUse hook delivers text to the agent without
// ALSO refusing the action — measured through this same mock, one channel per
// run: stdout and stderr at exit 0, stderr at exits 1, 3, 126 and 127,
// `systemMessage` and `additionalContext` are all swallowed; only stderr at exit
// 2 and the JSON `permissionDecision: "deny"` arrive, and both refuse.
//
// An earlier version of this comment said stderr reaches the agent "when the
// hook exits non-zero" and claimed to have probed every other channel. Both
// halves were wrong: it is exit 2 specifically, not any non-zero status, and the
// unprobed channel was `permissionDecision` — the one the engine itself uses.
// The conclusion was right anyway, which is exactly why it went unchecked. See
// refuseForBroken in services/sloprail for the full table.
//
// So "warn and proceed" is indistinguishable from the original silence at the
// only place that matters. The refusal is the diagnostic's delivery mechanism,
// not a separate decision.
func TestT013_01_MisspelledMatcherFieldStopsTheWriteItGuarded(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "typo", misspelledField, map[string]string{
		"refuse.sh": refuseScript,
	})

	got := e.Run(proj, "s-013-01", "write a note", Turns("done",
		Write("w1", "guarded/notes.md", "hello"),
	))

	if got.Permitted() {
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

	if got.Permitted() {
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

	if got.Permitted() {
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

// T013_07: a declaration that cannot be parsed at all refuses, rather than
// going silent.
//
// This is the finding, and it is the round-one failure mode preserved for the
// case where the file is MOST broken. `AffectedKinds` correctly returns nothing
// for an unparseable declaration — there are no bindings to read off a file that
// did not parse — but nothing else reported it either, and the name never
// reached the stream. Announced once at session start, silent at every action
// after it.
//
// "Genuinely a warning" was the framing, and the channel table makes it false:
// no channel at this hook point delivers text to the agent without also refusing
// the action, so "warn and proceed" IS "proceed". The more broken the file, the
// quieter the engine got.
//
// Note this is deliberately not scoped. There is no evidence of what the file
// was guarding, and "no evidence of what it guarded" is not "evidence it guarded
// nothing" — see refuseForUnreadable.
func TestT013_07_AnUnparseableDeclarationRefusesRatherThanGoingSilent(t *testing.T) {
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

			if got.Permitted() {
				t.Fatalf("a declaration too broken to read was treated as guarding nothing:\n%s", got.Output)
			}
			// The name is the whole point: it is what a person needs to find the
			// file, and it was the thing that never reached the stream.
			if !got.Saw("unreadable") {
				t.Errorf("the refusal does not name the declaration that could not be read:\n%s", got.Output)
			}
			// A refusal an author cannot clear is a trap, and this one fires on
			// every action until the file parses.
			if !got.Saw("remove that folder") {
				t.Errorf("the refusal does not say how to get unstuck:\n%s", got.Output)
			}
		})
	}
}

// T013_08: the refusal does not lock the project out of fixing it.
//
// T013_07 refuses every action, which is the strongest response in this file and
// the one that would be a trap if the way out were also refused. It is not: the
// refusal names the file and says to fix or remove it, and both are done outside
// the session. What must not happen is the engine ALSO failing to explain
// itself — a blanket refusal carrying no name would leave an author with a
// project that refuses everything and no idea which folder to look in.
//
// So this asserts the reason travels with the refusal on an unrelated action,
// which is where a scoped refusal would have said nothing at all.
func TestT013_08_TheBlanketRefusalAlwaysExplainsItself(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "unreadable", unparseable, nil)

	// A command, not a write: a different module entirely, so nothing about
	// this action is related to the file that cannot be read.
	got := e.Run(proj, "s-013-08", "list the files", Turns("done",
		Bash("b1", "ls -la"),
	))

	if got.Permitted() {
		t.Fatalf("an unreadable declaration was silent on an action of another kind:\n%s", got.Output)
	}
	if !got.Saw("unreadable") {
		t.Errorf("the refusal does not name the file to fix:\n%s", got.Output)
	}
	if !got.Saw("could not be read at all") {
		t.Errorf("the refusal does not say what is wrong:\n%s", got.Output)
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
