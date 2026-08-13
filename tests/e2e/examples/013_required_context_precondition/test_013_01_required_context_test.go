package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The shipped example, driven as a user would meet it.
//
// Every test in this file installs examples/guardrails/required-context-
// precondition verbatim. Nothing here restates the declaration or the hook: a
// test carrying its own copy would prove the copy works and say nothing about
// the files a user lifts.
//
// ---------------------------------------------------------------------------
// WHAT RUNS TODAY, AND WHAT DOES NOT
//
// The example reads the session's trajectory through `sloprail session query`,
// which is told which record to read via a transcript_path on its stdin. A hook
// is handed {event, guardrailDir} and nothing naming the session's record, so
// the example takes its cannot-check path and refuses.
//
// So the tests below split in two:
//
//   - T013_01..03 run today. They prove the binding (which writes reach the
//     hook at all) and the fail-closed behaviour (an unrunnable check refuses
//     rather than permits). Those are properties of the example as shipped, not
//     placeholders — the refusal they assert on is the one a user gets.
//
//   - T013_04..05 are the halves that need the trajectory: that a session which
//     loaded the skill is PERMITTED, and that loading the wrong skill is not
//     enough. They are skipped, and named honestly as zero coverage until the
//     dependency lands.
// ---------------------------------------------------------------------------

const (
	guardedTopic    = "memories/topics/no-slop/TOPIC.md"
	guardedDecision = "memories/decisions/pricing/DECISION.md"
	unguarded       = "memories/updates/2026-08-13_daily-checkin.md"
)

// refusalText is the sentence the example refuses with when it cannot read the
// record. Matched on rather than on the word "denied", so a test cannot pass on
// a refusal that came from somewhere else in the engine.
const cannotCheckRefusal = "a precondition that could not be checked is not a precondition that passed"

// skillRequiredRefusal is what the example says when the record was read and
// the skill was absent from it. Distinct from the above on purpose: the two
// look identical from outside if a test only asks whether the write was
// blocked, and they mean completely different things about the rule.
const skillRequiredRefusal = "SKILL REQUIRED"

// T013_01: a write under a guarded prefix reaches the rule and is refused.
//
// The positive half of the binding. Nothing here calls sloprail: the agent
// writes, the harness fires PreToolUse, the plugin reaches the subcommand, and
// the refusal travels back in the tool result.
func TestT013_01_GuardedPrefixIsRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	installExample(t, proj)

	got := e.Run(proj, "s-013-01", "start a topic", Turns("done",
		harness.Write("w1", guardedTopic, "# no-slop"),
	))

	if !got.Saw("required-context-precondition") {
		t.Fatalf("the write under a guarded prefix never reached the rule:\n%s", got.Output)
	}
	// The write did not land. This is the claim the whole rule rests on, and it
	// is asserted on the tool result rather than on any wording — a refusal that
	// let the file through would still print a refusal message.
	if got.Saw("File written successfully") {
		t.Fatalf("the guarded write landed despite the refusal:\n%s", got.Output)
	}
	// The skill the prefix demands is named in the message. A refusal that did
	// not say which skill leaves the agent with nothing to act on, and the
	// example's rubric claims it does say.
	//
	// Deliberately NOT asserting which of the two refusals this is. Today the
	// record is unreadable and the example refuses for that reason; once a hook
	// can read the trajectory this same run refuses with SKILL REQUIRED
	// instead. Both are correct for a session that never loaded the skill, and a
	// test pinned to today's wording would start failing the moment the
	// dependency it is waiting for arrives — which is the opposite of what a
	// test guarding an example should do. Which refusal is which is asserted in
	// T013_06, where telling them apart is the point.
	if !got.Saw("document-topic") {
		t.Fatalf("the refusal did not name the required skill:\n%s", got.Output)
	}
}

// T013_06: today's refusal is the fail-closed one, for the stated reason.
//
// Pinned separately from T013_01 because it is a claim about the CURRENT
// environment rather than about the rule. The example documents that it cannot
// read the trajectory yet and therefore refuses without having checked; if that
// stopped being true and nobody noticed, its rubric would be describing a
// version of itself that no longer exists.
//
// This is the test that is EXPECTED to fail when the dependency lands, and the
// failure is the signal: delete it, lift the skips, and the example's
// SR_TRANSCRIPT section comes out with it.
func TestT013_06_TodayTheCheckCannotRunAndSoRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	installExample(t, proj)

	got := e.Run(proj, "s-013-06", "start a topic", Turns("done",
		harness.Write("w1", guardedTopic, "# no-slop"),
	))

	if !got.Saw(cannotCheckRefusal) {
		t.Fatalf("the example no longer takes its cannot-check path — a hook can now read the record, so lift the skips in T013_04/T013_05, delete this test, and remove the SR_TRANSCRIPT section from GUARDRAIL.md:\n%s", got.Output)
	}
	if got.Saw(skillRequiredRefusal) {
		t.Fatalf("the example reported having checked the trajectory, which it cannot do yet:\n%s", got.Output)
	}
}

// T013_02: the second guarded prefix demands its own skill, not the first's.
//
// A rule that mapped every guarded path to one skill would pass T013_01 and be
// wrong. This is what tells the two prefixes apart.
func TestT013_02_SecondPrefixDemandsItsOwnSkill(t *testing.T) {
	e := New(t)
	proj := e.Project()
	installExample(t, proj)

	got := e.Run(proj, "s-013-02", "record a decision", Turns("done",
		harness.Write("w1", guardedDecision, "# pricing"),
	))

	if !got.Saw("document-strategy") {
		t.Fatalf("a write under memories/decisions/ did not demand document-strategy:\n%s", got.Output)
	}
	if got.Saw("document-topic") {
		t.Fatalf("a write under memories/decisions/ demanded the topics skill:\n%s", got.Output)
	}
}

// T013_03: a write outside the guarded prefixes is left alone.
//
// The over-fire half, and the one that matters most here. The example currently
// refuses every guarded write for want of a readable record — so without this,
// a guardrail that refused EVERYTHING would pass T013_01 and T013_02 and look
// correct. This is what separates a rule with a scope from a rule that blocks
// all work.
func TestT013_03_UnguardedPathIsPermitted(t *testing.T) {
	e := New(t)
	proj := e.Project()
	installExample(t, proj)

	got := e.Run(proj, "s-013-03", "write a check-in", Turns("done",
		harness.Write("w1", unguarded, "# 2026-08-13"),
	))

	if got.Saw("required-context-precondition") {
		t.Fatalf("a path outside the guarded prefixes was judged by the rule:\n%s", got.Output)
	}
	if got.Saw(cannotCheckRefusal) || got.Saw(skillRequiredRefusal) {
		t.Fatalf("a path outside the guarded prefixes was refused:\n%s", got.Output)
	}
	// The write actually landed. Asserting only on the absence of a refusal
	// would also hold for a run where the agent never got as far as writing.
	if !got.Saw("File written successfully") {
		t.Fatalf("the unguarded write did not go through at all:\n%s", got.Output)
	}
}

// needsTrajectory skips a test that cannot run until a hook can read the
// session's record, and says exactly what is missing.
//
// DELETE THIS ONE LINE FROM A TEST'S BODY to lift its skip. Nothing else about
// the test changes: they are written against the behaviour the example already
// has, not against a behaviour still to be designed.
func needsTrajectory(t *testing.T) {
	t.Helper()
	t.Skip(`skipped: this example's hook cannot read the session's trajectory yet.

` + "`sloprail session query`" + ` is told which record to read via a transcript_path on
its stdin payload. A hook is handed {event, guardrailDir} and no environment at
all, so it has neither the path nor anything to derive one from, and the example
takes its cannot-check path on every write.

impl/hook-env is NECESSARY but NOT SUFFICIENT. It gives hooks SR_GUARDRAIL,
SR_SESSION_ID and SR_WORKSPACE — but SR_SESSION_ID is the STABLE session id, the
uuid of the conversation's root record, deliberately not the harness's own id
and therefore not the transcript's filename. Building a path from it lands on a
file that does not exist; that was checked against the branch, not assumed.

What is missing on top of impl/hook-env is a variable naming the record itself.
The example reads SR_TRANSCRIPT. Adding it is a one-line change beside the three
that branch already sets.

Until then this test is zero coverage. It is written and kept so that it starts
passing the moment the dependency lands — which was verified rather than hoped
for: both skipped tests were run green against a scratch build carrying
impl/hook-env plus that one line.

T013_06 is the tripwire. It pins today's behaviour and FAILS when the dependency
arrives, so nothing here can sit skipped after it stops needing to be.`)
}

// T013_04: a session that loaded the required skill is PERMITTED.
//
// The half that proves the rule is about the trajectory rather than about the
// path. Without it, every passing test in this file is satisfied by a guardrail
// that refuses every guarded write forever — which is precisely the shape the
// example has today, and precisely why this test's skip is not a formality.
//
// The agent loads document-topic, then writes under memories/topics/. Same path
// as T013_01, same rule, opposite verdict; the only difference between the two
// runs is what the agent did first.
func TestT013_04_LoadedSkillPermitsTheWrite(t *testing.T) {
	needsTrajectory(t)

	e := New(t)
	proj := e.Project()
	installExample(t, proj)

	got := e.Run(proj, "s-013-04", "start a topic properly", Turns("done",
		harness.Skill("s1", "document-topic"),
		harness.Write("w1", guardedTopic, "# no-slop"),
	))

	if got.Saw(skillRequiredRefusal) {
		t.Fatalf("the skill was loaded and the write was still refused:\n%s", got.Output)
	}
	if got.Saw(cannotCheckRefusal) {
		t.Fatalf("the rule still could not read the record:\n%s", got.Output)
	}
	if !got.Saw("File written successfully") {
		t.Fatalf("the write did not land after the skill was loaded:\n%s", got.Output)
	}
}

// T013_05: loading SOME skill is not loading THE skill.
//
// The rule must check which skill entered the trajectory, not merely that the
// Skill tool was used. A hook matching on the tool name alone would pass T013_04
// and hand every guarded folder to any agent that had loaded anything at all.
func TestT013_05_WrongSkillDoesNotSatisfy(t *testing.T) {
	needsTrajectory(t)

	e := New(t)
	proj := e.Project()
	installExample(t, proj)

	got := e.Run(proj, "s-013-05", "record a decision", Turns("done",
		harness.Skill("s1", "document-topic"),
		harness.Write("w1", guardedDecision, "# pricing"),
	))

	if !got.Saw(skillRequiredRefusal) {
		t.Fatalf("the topics skill satisfied a decisions write:\n%s", got.Output)
	}
	if !got.Saw("document-strategy") {
		t.Fatalf("the refusal did not name the skill actually required:\n%s", got.Output)
	}
}
