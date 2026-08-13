package e2e

import (
	"testing"
)

// verdict_is_binary: a hook either refuses the work or permits it, with no third
// outcome that records an objection while allowing the work to proceed.
//
// The spec's reasoning: "An advisory tier is where rules go to be ignored. A
// rule worth declaring is worth enforcing, and a warning an agent may disregard
// is indistinguishable from no rule at all."
//
// Why this needs an end-to-end test rather than a unit one. The engine's own
// `verdict` type is a bool and a string, so at that layer the invariant is true
// by construction and a unit test of it asserts a property of Go's type system.
// What the invariant is actually ABOUT is the boundary: every way a hook can
// answer must land on one of two outcomes, and the outcome must be the one the
// AGENT experiences. A hook that objects on a channel nobody delivers is an
// advisory tier arrived at by accident rather than by design, and only a test
// that drives a real hook and then asks whether the file exists can tell the
// difference.
//
// So each test below pairs the two observations that together exclude a third
// outcome:
//
//   - what reached the agent — Refused() reads the harness's own block marker,
//     never a word-scan of the stream
//   - what happened to the tree — Wrote() asks the filesystem whether the work
//     landed
//
// An advisory outcome is exactly the combination "the rule objected AND the work
// landed". Neither observation alone can catch it, which is why no assertion
// here stands on one.

// permitSilently exits zero saying nothing. The consent case.
const permitSilently = `#!/bin/sh
cat >/dev/null
exit 0
`

// objectLoudlyButExitZero is the advisory tier, if one existed: the hook states
// an objection on both streams and still exits zero.
//
// This is the fixture the invariant turns on. An engine with a warning tier
// would have to read this as "objected", and the only tier available to express
// that without blocking is one that lets the write land. The assertion is that
// the engine reads it as consent instead — silence and speech at exit zero are
// the same answer.
const objectLoudlyButExitZero = `#!/bin/sh
cat >/dev/null
echo 'this violates the rule'
echo 'this violates the rule' >&2
exit 0
`

// objectWithAStructuredReasonButExitZero is the same trap in the shape the
// engine parses on the REFUSING path.
//
// refusalReason reads {"reason": ...} off stdout to find a hook's own words. A
// hook that emits that structure and exits zero is the most plausible way an
// advisory tier would appear by accident — the payload the engine already knows
// how to read, on the status that means consent. The status must win.
const objectWithAStructuredReasonButExitZero = `#!/bin/sh
cat >/dev/null
printf '{"reason":"advisory: this file is not allowed"}\n'
exit 0
`

// refuse is the refusal case: non-zero, with something to say.
const refuse = `#!/bin/sh
cat >/dev/null
echo 'refused: this file is not allowed' >&2
exit 1
`

// bindPreFileCreate binds one hook to file creation, the before-timing point
// where a refusal PREVENTS the work — so "did the file land" is a direct read of
// which outcome the engine chose.
const bindPreFileCreate = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./judge.sh
---

# Judges every file this session creates
`

// T016_01: the control — a permitting hook lets the work land, unrefused.
//
// Without this, every "the work landed" assertion below would hold for an engine
// that never dispatches to this binding at all, and the suite would report that
// no advisory tier exists because no rule ever ran.
func TestT016_01_APermittingHookLetsTheWorkLand(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", bindPreFileCreate, map[string]string{"judge.sh": permitSilently})

	got := e.Run(proj, "s-016-01", "write a note", Turns("done",
		Write("w1", "notes.md", "hello\n"),
	))

	if got.Refused() {
		t.Fatalf("a hook that exited zero was read as a refusal:\n%s", got.Output)
	}
	if !e.Wrote(proj, "notes.md") {
		t.Fatalf("a permitted write did not land — this binding is not being dispatched to, "+
			"so nothing below is evidence about verdicts:\n%s", got.Output)
	}
}

// T016_02: the other pole — a refusing hook stops the work.
//
// The second half of the control. Together with T016_01 it establishes that this
// engine really does reach both outcomes, which is what makes "there is no
// third" a claim about a populated space rather than an empty one.
func TestT016_02_ARefusingHookStopsTheWork(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", bindPreFileCreate, map[string]string{"judge.sh": refuse})

	got := e.Run(proj, "s-016-02", "write a note", Turns("done",
		Write("w1", "notes.md", "hello\n"),
	))

	if !got.Refused() {
		t.Fatalf("a hook that exited non-zero did not refuse the work:\n%s", got.Output)
	}
	if e.Wrote(proj, "notes.md") {
		t.Fatalf("a refusal at a before-timing point did not prevent the write — " +
			"the file is on disk, so the refusal was advisory in effect")
	}
}

// T016_03: a hook that objects in prose but exits zero PERMITS, and the work
// lands.
//
// The invariant proper. There is no outcome in which the objection is recorded
// and the work still proceeds: the engine either refuses (and the file is
// absent) or permits (and the objection has no effect at all). This asserts the
// second, which is the only one available at exit zero.
//
// Both halves are asserted because either alone admits an advisory reading. If
// only the file were checked, an engine that blocked the turn while still
// writing the file would pass. If only the refusal marker were checked, an
// engine that refused and silently discarded the write would pass.
func TestT016_03_AnObjectionAtExitZeroIsConsentNotAWarning(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", bindPreFileCreate, map[string]string{"judge.sh": objectLoudlyButExitZero})

	got := e.Run(proj, "s-016-03", "write a note", Turns("done",
		Write("w1", "notes.md", "hello\n"),
	))

	if got.Refused() {
		t.Fatalf("a hook that exited zero was treated as refusing because it wrote an objection — "+
			"speech at exit zero must be consent, or a rule's diagnostics become a block:\n%s", got.Output)
	}
	if !e.Wrote(proj, "notes.md") {
		t.Fatalf("a hook that exited zero did not let the work land — the objection it wrote "+
			"changed the outcome, which is the advisory tier the spec excludes:\n%s", got.Output)
	}
}

// T016_04: the same at the structured channel the engine already parses.
//
// A {"reason": ...} on stdout is what a refusing hook uses to give the agent its
// own words. Emitting it at exit zero is the shape an advisory tier would most
// plausibly take, because every piece of machinery it needs already exists — the
// engine can read the reason, and only the exit status says not to act on it.
//
// The claim is that the status decides and the payload does not. An engine that
// promoted this to a refusal would have a third outcome in all but name: a hook
// could then object without committing to blocking, which is exactly the tier a
// rule goes to be ignored in.
func TestT016_04_AStructuredReasonAtExitZeroIsStillConsent(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", bindPreFileCreate,
		map[string]string{"judge.sh": objectWithAStructuredReasonButExitZero})

	got := e.Run(proj, "s-016-04", "write a note", Turns("done",
		Write("w1", "notes.md", "hello\n"),
	))

	if got.Refused() {
		t.Fatalf("a structured reason at exit zero was promoted to a refusal — the exit status "+
			"is what decides, or a hook can object without committing to blocking:\n%s", got.Output)
	}
	if !e.Wrote(proj, "notes.md") {
		t.Fatalf("a hook that exited zero did not let the work land:\n%s", got.Output)
	}
}

// T016_05: two hooks, one objecting at exit zero and one refusing, still produce
// exactly one of the two outcomes.
//
// The composition case. If an advisory tier existed anywhere it would most
// likely appear when outcomes have to be COMBINED — a warning from one hook and
// a refusal from another giving something that is neither. Here the refusal must
// win outright and the objection must contribute nothing: the work is stopped,
// and it is stopped for the refusing rule's reason.
func TestT016_05_AnObjectionCombinedWithARefusalIsJustARefusal(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "adviser", bindPreFileCreate,
		map[string]string{"judge.sh": objectLoudlyButExitZero})
	e.Guardrail(proj, "blocker", bindPreFileCreate, map[string]string{"judge.sh": refuse})

	got := e.Run(proj, "s-016-05", "write a note", Turns("done",
		Write("w1", "notes.md", "hello\n"),
	))

	if !got.Refused() {
		t.Fatalf("a refusing rule did not stop the work when another rule merely objected:\n%s", got.Output)
	}
	if e.Wrote(proj, "notes.md") {
		t.Fatalf("the file landed despite a refusal — combining an objection with a refusal " +
			"produced something that is neither outcome")
	}
}
