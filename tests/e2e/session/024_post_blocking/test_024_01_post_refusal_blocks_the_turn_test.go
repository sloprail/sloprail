package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// What a Post refusal can and cannot do.
//
// TWO FACTS, and conflating them was a real design error before the owner
// corrected it:
//
//   - It cannot undo the write. The file is on disk and the cycle is over; an
//     engine claiming otherwise would be promising a rollback it never
//     performed.
//   - It MUST refuse the work. That is the entire mechanism by which
//     an after-the-fact rule gets a correction rather than merely complaining
//     once.
//
// Every test here asserts them SEPARATELY, because the file surviving is not
// evidence of anything: it survives whether the engine blocks or silently
// permits, so a test asserting only that passes against an engine with no
// blocking at all.
//
// HOW A REFUSAL IS OBSERVED. A file-guard is judged by `sr check run --base
// --head` over the commits the session made, not at Stop; the harness runs it
// after every Run and BlockingErrors returns its refusals alongside the Stop's
// own. "The turn is blocked" is now "the session's range is refused".

// refuseCreates is a NEW-FORMAT file-guard, after-check (a file-guard acts only at Stop),
// that objects to every markdown file the cycle produces . An
// after-check refusal is exactly this directory's subject: it does not undo the
// write (the file is on disk), it holds the TURN, and it re-fires next cycle —
// which is the whole mechanism a Post refusal enforces a correction with. `match:
// "**/*.md"` fires on every committed change; every file this
// directory writes is `.md`.
const refuseCreates = `match: "**/*.md"
checks:
  - script: ./refuse.sh
`

// refused is whether a file-guard refused the session's work. A file-guard no
// longer holds the turn at Stop: it is judged by `sr check run` over the
// commits the session made, which the harness runs after every Run
// (harness.FileGuardRefusals).
func refused(e *harness.Env, proj, sess string) bool {
	return len(e.FileGuardRefusals(proj, sess)) > 0
}

// refusingGuardrail writes a file-guard whose check logs OUTSIDE the project and
// then refuses.
//
// Outside deliberately. A check's working directory is its guard's folder, which
// sits INSIDE the tree being compared — so a check logging there creates an
// untracked file, which the same cycle reports and this same rule then refuses.
// One rule objecting to one file quietly becomes two objections, which takes the
// multi-refusal path and leaves the single-refusal wording untested.
//
// The refusal is the NEW-FORMAT contract: exit non-zero refuses, and the reason is
// a `{"reason":…}` object on stdout (scriptRefusalReason prefers structured
// stdout). This is what carries the message to the agent, as a blocking error, in
// place of the old exit-with-stderr channel.
func refusingGuardrail(t *testing.T, e *harness.Env, proj, name, message string) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), name+".log")
	e.FileGuard(proj, name, refuseCreates, map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho ran >> " + log + "\n" +
			"echo " + shq(`{"reason":"`+message+`"}`) + "\nexit 1\n",
	})
	return log
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// project is a repository whose guardrails are committed before the session, so
// the rules' own folders are part of the baseline.
func project(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	return e, proj
}

// T024_01: one refusal — the file survives AND the turn is blocked AND the
// reason reaches the agent.
//
// Three assertions, each of which can fail on its own. The file check alone
// would pass against an engine that never blocked; the block alone would pass
// against one that blocked with no words, leaving the agent stopped with nothing
// to act on — worse than permitting, because now it is stuck as well as
// uninformed.
func TestT024_01_OneRefusalBlocksTheTurnWithoutUndoingTheWrite(t *testing.T) {
	e, proj := project(t)
	ranLog := refusingGuardrail(t, e, proj, "solorule", "this file should not have been written")
	e.CommitAll(proj, "the project before the session")

	got := e.Run(proj, "s-024-01", "write a file", Turns("done",
		Write("w1", "unwanted.md", "it landed anyway\n"),
	).ThenCommit("the agent's work"))

	// The rule ran and refused. Without this the rest is a test about a file
	// existing after nothing tried to stop it.
	if _, err := os.Stat(ranLog); err != nil {
		t.Fatalf("the refusing Post hook never ran, so this proves nothing about after-the-fact "+
			"refusals:\n%s", got.Output)
	}

	// FACT ONE: the write stands. A Post event describes work that has already
	// landed; the refusal demands a correction, it does not perform one.
	if !e.Exists(proj, "unwanted.md") {
		t.Errorf("a hook refusing AFTER the write removed the file — an after-the-fact refusal " +
			"must demand a correction, not perform one")
	}

	// FACT TWO: the work was refused.
	if !refused(e, proj, "s-024-01") {
		t.Errorf("the session's work passed despite a guardrail refusing — a refusal "+
			"is the only way it gets anything corrected:\n%s", got.Output)
	}

	// FACT THREE: the agent was told why, and by whom. Read from the blocking
	// attachments rather than the record as a whole — a guardrail's folder path
	// travels on every hook payload, so searching the file for the rule's name
	// finds it whether or not the refusal ever named it.
	blocking := e.BlockingErrors(proj, "s-024-01")
	if len(blocking) == 0 {
		t.Fatalf("the turn was blocked but no reason reached the agent — it is stopped with " +
			"nothing to act on")
	}
	told := strings.Join(blocking, "\n")
	if !strings.Contains(told, "this file should not have been written") {
		t.Errorf("the hook's own words did not reach the agent, so it cannot know what to fix:\n%s", told)
	}
	if !strings.Contains(told, "solorule") {
		t.Errorf("the refusal did not name the guardrail that produced it — an agent told only "+
			"that it was blocked cannot find the rule it broke:\n%s", told)
	}
}

// T024_02: several refusals in one cycle — all reported, blocked once.
//
// Three rules refuse the same file. Every objection must reach the agent
// TOGETHER: handed one at a time, a correction takes as many turns as there are
// rules, and the rules bound after the first would not have run at all, so the
// agent could not even see what else is wrong.
//
// "Blocked once at the end" is asserted as its own fact: one blocking
// attachment carrying three reasons, not three separate blocks.
func TestT024_02_SeveralRefusalsAreAllReportedAndBlockOnce(t *testing.T) {
	e, proj := project(t)
	logs := map[string]string{}
	for _, name := range []string{"alpharule", "betarule", "gammarule"} {
		logs[name] = refusingGuardrail(t, e, proj, name, "objection from "+name)
	}
	e.CommitAll(proj, "the project before the session")

	got := e.Run(proj, "s-024-02", "write a file", Turns("done",
		Write("w1", "contested.md", "one file, three objections\n"),
	).ThenCommit("the agent's work"))

	// Every rule really ran. Without this, "all three were reported" could hold
	// because only one ran and the assertion below is checking a string that
	// happens to contain three names.
	for name, log := range logs {
		if _, err := os.Stat(log); err != nil {
			t.Fatalf("guardrail %q never ran, so this is not a multi-refusal cycle:\n%s", name, got.Output)
		}
	}

	if !refused(e, proj, "s-024-02") {
		t.Fatalf("three guardrails refused and the work was not refused:\n%s", got.Output)
	}

	blocking := e.BlockingErrors(proj, "s-024-02")
	if len(blocking) == 0 {
		t.Fatalf("three guardrails refused and no reason reached the agent")
	}
	told := strings.Join(blocking, "\n")
	for name := range logs {
		if !strings.Contains(told, name) {
			t.Errorf("guardrail %q's objection is missing from what the agent was told — "+
				"blocking at the first hides the rest, and the agent fixes them one turn at a "+
				"time:\n%s", name, told)
		}
	}
	// The write still stands, three refusals or one.
	if !e.Exists(proj, "contested.md") {
		t.Errorf("the file was removed by after-the-fact refusals")
	}
}

// T024_03: a refusal does not silence the other rules in the same cycle.
//
// The dispatcher collects objections rather than returning at the first, so a
// second rule still runs after one has refused. Without this case the suite cannot
// tell an engine that dispatches everything from one that abandons the cycle the
// moment a hook objects — the file survives either way, and so does the block.
//
// Under the new dispatch the "runs after the refuser" rule is a SECOND file-guard:
// runFileGuardsPost iterates EVERY file-guard against the cycle's Post events and
// keeps going past a refusal (it appends the objection and continues judging the
// rest), so a passing guard is asked even when a sibling refused the same write.
// That is the same "collect, do not abandon" property the old Stop-bound rule
// proved — a "stop at the first refusal" engine still loses the passer here.
func TestT024_03_ARefusalDoesNotSilenceThePassingRuleAfterIt(t *testing.T) {
	e, proj := project(t)
	ranLog := refusingGuardrail(t, e, proj, "objector", "this one refuses")

	afterLog := filepath.Join(t.TempDir(), "after.log")
	e.FileGuard(proj, "afterwards", `match: "**/*.md"
checks:
  - script: ./record.sh
`, map[string]string{"record.sh": "#!/bin/sh\ncat >/dev/null\necho ran >> " + afterLog + "\nexit 0\n"})
	e.CommitAll(proj, "the project before the session")

	got := e.Run(proj, "s-024-03", "write a file", Turns("done",
		Write("w1", "watched.md", "the subject\n"),
	).ThenCommit("the agent's work"))

	if _, err := os.Stat(ranLog); err != nil {
		t.Fatalf("the refusing hook never ran, so there is no refusal here to survive:\n%s", got.Output)
	}
	if !refused(e, proj, "s-024-03") {
		t.Fatalf("the work passed despite a refusal, so this is not the case being tested:\n%s", got.Output)
	}
	if _, err := os.Stat(afterLog); err != nil {
		t.Fatalf("a rule bound after the refusing one never ran — one guardrail objecting " +
			"silenced the rest of the cycle, and the agent is fixing violations one turn at a time")
	}
}

// T024_05: a rule that PASSES is not reported as an objection.
//
// The negative control for the whole directory. Everything above asserts that
// refusals travel; this asserts that a cycle where nothing objects is not
// blocked and produces no blocking attachment — otherwise "the turn was blocked"
// could be true of every cycle and the assertions above would be measuring
// nothing.
//
// The rule is the same shape as the refusing ones and runs on the same event;
// only its exit code differs.
func TestT024_05_APassingRuleDoesNotBlockTheTurn(t *testing.T) {
	e, proj := project(t)
	ranLog := filepath.Join(t.TempDir(), "passer.log")
	e.FileGuard(proj, "passer", refuseCreates, map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho ran >> " + ranLog + "\nexit 0\n",
	})
	e.CommitAll(proj, "the project before the session")

	got := e.Run(proj, "s-024-05", "write a file", Turns("done",
		Write("w1", "fine.md", "nothing wrong with this\n"),
	).ThenCommit("the agent's work"))

	// The rule ran. Without this the absence of a block says only that no rule
	// was ever consulted.
	if _, err := os.Stat(ranLog); err != nil {
		t.Fatalf("the passing hook never ran, so the absence of a block below proves nothing:\n%s",
			got.Output)
	}
	if refused(e, proj, "s-024-05") {
		t.Errorf("a cycle in which every rule passed was refused anyway:\n%s", got.Output)
	}
	if b := e.BlockingErrors(proj, "s-024-05"); len(b) > 0 {
		t.Errorf("a cycle in which every rule passed produced a blocking reason:\n%s",
			strings.Join(b, "\n"))
	}
}

// T024_06: a refusal and a pass in one cycle — only the refuser is named.
//
// The mixed case. Two rules bound to the same event, one refusing and one
// passing, and the blocking reason must name the refuser and NOT the passer. An
// engine that reported every rule it consulted would send the agent looking for
// a violation in a rule that permitted the work.
//
// Both rules are proven to have run, so "the passer is not named" is about the
// reporting rather than about a rule that never fired.
func TestT024_06_ARefusalAndAPassNameOnlyTheRefuser(t *testing.T) {
	e, proj := project(t)
	refuserLog := refusingGuardrail(t, e, proj, "zzrefuser", "this is the objection")

	passerLog := filepath.Join(t.TempDir(), "passer.log")
	e.FileGuard(proj, "zzpermitter", refuseCreates, map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho ran >> " + passerLog + "\nexit 0\n",
	})
	e.CommitAll(proj, "the project before the session")

	got := e.Run(proj, "s-024-06", "write a file", Turns("done",
		Write("w1", "mixed.md", "one rule objects, one does not\n"),
	).ThenCommit("the agent's work"))

	for name, log := range map[string]string{"zzrefuser": refuserLog, "zzpermitter": passerLog} {
		if _, err := os.Stat(log); err != nil {
			t.Fatalf("guardrail %q never ran, so this is not the mixed case:\n%s", name, got.Output)
		}
	}
	if !refused(e, proj, "s-024-06") {
		t.Fatalf("the work passed despite one rule refusing:\n%s", got.Output)
	}

	blocking := e.BlockingErrors(proj, "s-024-06")
	if len(blocking) == 0 {
		t.Fatalf("a rule refused and no reason reached the agent")
	}
	told := strings.Join(blocking, "\n")
	if !strings.Contains(told, "zzrefuser") {
		t.Errorf("the refusing guardrail was not named in what the agent was told:\n%s", told)
	}
	if strings.Contains(told, "zzpermitter") {
		t.Errorf("a guardrail that PERMITTED the work was named in the refusal:\n%s\n"+
			"the agent is sent looking for a violation in a rule that had none", told)
	}
}

// T024_07: an unfixed refusal blocks the NEXT cycle too.
//
// The multi-cycle form, and the whole point of retaining a refusal. The first
// cycle writes a file the rule objects to; the second cycle does something else
// entirely and must STILL be blocked, because the offending file is still there
// and still unfixed.
//
// This is what makes an after-the-fact rule an enforcement mechanism rather than
// a complaint: the row fails the passing half of the exemption for as long as
// the content stays as it is, so the hook is asked again, refuses again, and
// blocks again until the agent changes the file.
func TestT024_07_AnUnfixedRefusalBlocksTheNextCycleToo(t *testing.T) {
	e, proj := project(t)
	ranLog := refusingGuardrail(t, e, proj, "persistent", "still not acceptable")
	e.CommitAll(proj, "the project before the session")

	const sess = "s-024-07"
	first := e.Run(proj, sess, "write the bad file", Turns("done",
		Write("w1", "offending.md", "violates\n"),
	).ThenCommit("the agent's work"))
	if _, err := os.Stat(ranLog); err != nil {
		t.Fatalf("the rule never ran in the first cycle:\n%s", first.Output)
	}
	if !refused(e, proj, sess) {
		t.Fatalf("the first cycle was not refused, so there is no outstanding refusal:\n%s", first.Output)
	}

	// A second cycle that does not touch the offending file at all.
	second := e.Run(proj, sess, "do something else", Turns("done",
		Write("w2", "unrelated.md", "fine\n"),
	).ThenCommit("the agent's work"))

	if len(e.CheckRun(proj, sess)) == 0 {
		t.Fatalf("a cycle that left an unfixed violation in place passed:\n%s\n"+
			"the file is still broken and the rule has stopped saying so — an after-the-fact "+
			"rule that complains once is not enforcement", second.Output)
	}
	if !e.Exists(proj, "offending.md") {
		t.Errorf("the offending file was removed, so the second block is not about it")
	}
}
