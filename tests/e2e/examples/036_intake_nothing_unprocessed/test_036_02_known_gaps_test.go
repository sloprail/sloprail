package e2e

import (
	"strings"
	"testing"
)

// This file PINS two shipped-example bugs in intake-nothing-unprocessed with
// tests that assert the CURRENT behavior and name the defect, so the suite is
// truthful about what the lifted file does rather than asserting an intended
// behavior the file does not deliver. Neither is an engine bug — the known-good
// eval-loop-maxing example demonstrates the correct patterns (paths anchored on
// $SR_WORKSPACE; a gate reading a context via .context[name].payload) that these
// example scripts do not use. See the package report.

// T036_04: the SHIPPED "map to a task" path does NOT admit, because the gate
// greps tasks/ relative to its own working directory (the guardrail folder),
// while the example expects the agent to write tasks/ at the REPOSITORY ROOT.
//
// This is the cwd bug: the engine runs a check with cwd = the guardrail's folder
// (.sloprail/gate/verify-intake-complete/), and verify-no-residue.sh's
// `grep -r tasks/` / `grep -r updates/ decisions/` are cwd-relative. A task file
// written at the repo root — where a user following the example would put it —
// is never seen, so the residue never empties and the gate refuses a session the
// author intended to pass. The fix the example needs is `$SR_WORKSPACE/tasks/`,
// exactly what eval-loop-maxing does for its own paths.
//
// The test asserts the CURRENT (buggy) outcome: a repo-root task file does NOT
// prevent the refusal. If the example is later fixed to anchor on $SR_WORKSPACE,
// THIS test flips (the write would then admit) and should be updated to the
// admit assertion — the failure will point here.
func TestT036_04_RepoRootTaskFileDoesNotAdmit_ExampleBug(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)

	sess := "s-036-04"
	ref := e.TranscriptPath(proj, sess) + ":1-1"
	// A task file at the REPOSITORY ROOT (where the example tells the agent to put
	// it), referencing the message ref correctly.
	e.WriteFile(proj, "tasks/task-a/ASK.md", "# Task A\n\nUser request ("+ref+").\n")
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install + repo-root task")

	res := e.Run(proj, sess, "please handle request A", Turns("done",
		Say("m1", "Recorded it as task-a at the repo root."),
	))

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("EXPECTED the shipped example to still refuse (cwd bug: the gate greps tasks/ "+
			"relative to the guardrail folder, so a repo-root task file is invisible). It did NOT "+
			"refuse — the example may have been fixed to anchor on $SR_WORKSPACE; if so, update this "+
			"test to assert admit.\n%s", res.Output)
	}
	if !strings.Contains(strings.Join(blocks, "\n"), residueReason) {
		t.Errorf("refused, but not with the residue reason — a different failure than the cwd bug:\n%v", blocks)
	}
}

// T036_05: the SHIPPED "explicit skip" path does NOT work, because the agent
// cannot run `sr-session state set skip:<ref> <reason>` from its own shell — that
// command requires the SR_GUARDRAIL hook environment the engine sets only when it
// runs a HOOK, and the agent's Bash turn has none.
//
// The example's own comment (verify-no-residue.sh) says the agent "just uses that
// sr-session state" to mark a message needing no task. But `state set` calls
// openSessionState(), which refuses with "no guardrail in scope — SR_GUARDRAIL is
// set by the engine when it runs a hook" whenever SR_GUARDRAIL is unset. So the
// skip is never recorded, the message stays unaccounted, and the gate refuses a
// session the author intended to pass with an explicit skip.
//
// This test asserts the CURRENT behavior: the agent's `state set skip:` errors,
// and the gate still refuses. It is a real defect in the example's DESIGN (the
// skip channel is incompatible with the engine's per-hook state scoping), not a
// mock limitation — the same `state set` fails identically outside the mock.
func TestT036_05_AgentSkipCannotBeRecorded_ExampleBug(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-036-05"
	ref := e.TranscriptPath(proj, sess) + ":1-1"
	res := e.Run(proj, sess, "just saying hi, no task needed", Turns("done",
		Bash("b1", "sr-session state set 'skip:"+ref+"' 'a greeting, no task needed'"),
	))

	// The agent's own state set failed for lack of a guardrail scope — the reason
	// travels back in the tool result the agent sees.
	if !res.Saw("no guardrail in scope") {
		t.Errorf("expected the agent's `sr-session state set skip:` to fail with "+
			"'no guardrail in scope' (it runs without the SR_GUARDRAIL hook env), but that "+
			"error did not appear — the skip channel may now work; if so, update this test:\n%s", res.Output)
	}
	// And because the skip was never recorded, the message is still unaccounted and
	// the gate refuses.
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 || !strings.Contains(strings.Join(blocks, "\n"), residueReason) {
		t.Errorf("expected the gate to still refuse (the skip did not land), got blocks: %v", blocks)
	}
}
