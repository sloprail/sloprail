package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// A sub-agent cannot ask the user, so when its change must cite the user's words it hands the
// change back: a patch file outside the repository, a revert on its branch, a report to its
// parent, who asks the user and commits the patch with the user's answer as the trailer.

const handbackPrompt = "please document the release process in docs"

// subagentStopNow runs the SubagentStop hook for the sub-agent the session dispatched into its
// worktree wt, as the harness does when that sub-agent ends its turn.
func subagentStopNow(t *testing.T, e *Env, proj, sess, wt, agent string) harness.Result {
	t.Helper()
	recs := e.SubagentRecordPaths(proj, sess)
	if len(recs) != 1 {
		t.Fatalf("premise: want one sub-agent record, have %v", recs)
	}
	payload, _ := json.Marshal(map[string]any{
		"session_id": sess, "transcript_path": e.TranscriptPath(proj, sess), "cwd": wt,
		"agent_id": agent, "agent_transcript_path": recs[0], "hook_event_name": "SubagentStop",
	})
	e.JudgeTracked(wt, sess, true) // the judging a real sub-agent asks for before it stops
	return e.CLIDirectStdinEnv(wt, string(payload), e.SessionEnv(""), "sr-session", "subagent-stop")
}

// T003_75: the refusal names commands that work. Backing up and reverting leaves the sub-agent's
// branch clean (its Stop passes); the parent applies the patch cleanly, is refused for it
// uncited, and passes once the commit carries the user's answer as the trailer.
// sr:proves subagents/citation-refusal-hands-the-change-back
func TestT003_75_ASubagentHandsUncitedChangesBackAndTheParentReappliesThemCited(t *testing.T) {
	e, proj, _ := uncitedProject(t, citingRule)
	const sess = "s-003-75"
	sub := harness.SubagentScript(t, Turns("sub done",
		harness.CommitFile("c1", "docs/release.md", "the steps", "document the release"),
	))
	res := e.Run(proj, sess, handbackPrompt, Turns("root done", harness.Dispatch("d1", "write the docs", sub, harness.OwnTree(t))))
	if !harness.HasCap(t, harness.CapWorktrees) {
		// A sub-agent in the root's tree is not refused at its own Stop: its uncited commit is
		// the root's to answer for, so there is no hand-back. The root is told, plainly, to cite
		// (T003_75 root), and passes once the commit carries the user's answer.
		if !e.NoSubagentStopBlock(proj, sess) {
			t.Fatalf("a sub-agent in the root's tree was refused for its uncited commit:\n%s", res.Output)
		}
		told := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
		if !strings.Contains(told, "docs/release.md") || !strings.Contains(told, "Sloprail-Cites-User: <exact quote>") || strings.Contains(told, "You are a sub-agent") {
			t.Fatalf("the root's refusal for the sub-agent's uncited commit is not the plain cite instruction:\n%s", told)
		}
		blocks := stopBlocks(e, proj, sess)
		e.Run(proj, sess, "cited", Turns("cited",
			Bash("a2", "git commit -q --amend -m 'document the release' -m 'Sloprail-Cites-User: "+handbackPrompt+"'"),
		))
		if n := stopBlocks(e, proj, sess); n > blocks {
			t.Fatalf("the root's cited commit was refused:\n%s", newBlocks(e, proj, sess, blocks))
		}
		return
	}
	if !e.SubagentStopBlocked(proj, sess, "") {
		t.Fatalf("premise: the sub-agent's uncited commit should be refused at its Stop:\n%s", res.Output)
	}
	told := strings.Join(e.SubagentBlockingErrors(proj, sess), "\n")
	for _, want := range []string{
		"You are a sub-agent", "cannot get the user's words yourself", "docs/release.md",
		"Sloprail-Cites-User: <the user's exact answer>", "EXACTLY what needs the user's approval",
	} {
		if !strings.Contains(told, want) {
			t.Fatalf("the sub-agent's refusal lacks %q:\n%s", want, told)
		}
	}
	for _, not := range []string{"backup/", "git branch", "git stash push"} {
		if strings.Contains(told, not) {
			t.Fatalf("the refusal recommends %q, which is itself a recorded ref or hidden work:\n%s", not, told)
		}
	}
	wt, agent := subagentFolder(t, e, proj, sess)
	scratch := t.TempDir()
	run := func(dir, command string) { runCommand(t, dir, "export TMPDIR='"+scratch+"'; "+command) }

	// Still refused until the commands are run.
	if r := subagentStopNow(t, e, proj, sess, wt, agent); !harness.Blocked(r) {
		t.Fatalf("the sub-agent's Stop passed with its uncited commit still on the branch:\n%s", r.Output)
	}

	// The commands it names, exactly as written.
	run(wt, commandIn(t, told, "git diff --binary"))
	patch := filepath.Join(scratch, "handback.patch")
	if b, err := os.ReadFile(patch); err != nil || !strings.Contains(string(b), "docs/release.md") {
		t.Fatalf("the saved patch is missing or lacks the change: %v\n%s", err, b)
	}
	run(wt, commandIn(t, told, "git apply -R"))
	if _, err := os.Stat(filepath.Join(wt, "docs", "release.md")); err == nil {
		t.Fatal("the revert left the file on the sub-agent's branch")
	}
	if r := subagentStopNow(t, e, proj, sess, wt, agent); harness.Blocked(r) {
		t.Fatalf("after the backup and revert the sub-agent's Stop was still refused:\n%s", r.Output)
	}
	// The parent's Stop verifies the sub-agent's branch now: reverted, it holds nothing uncited.
	if r := e.StopNow(proj, sess, false); harness.Blocked(r) {
		t.Fatalf("the parent was refused for the sub-agent's reverted work:\n%s", r.Output)
	}

	// The parent re-applies the patch: uncited, refused; with the user's answer, passed.
	blocks := stopBlocks(e, proj, sess)
	e.Run(proj, sess, "apply the handed-back patch", Turns("applied",
		Bash("a1", "git apply --index '"+patch+"' && git commit -q -m 'document the release'"),
	))
	if stopBlocks(e, proj, sess) <= blocks {
		t.Fatal("the parent's commit of the patch with no citation was not refused")
	}
	blocks = stopBlocks(e, proj, sess)
	e.Run(proj, sess, "cited", Turns("cited",
		Bash("a2", "git commit -q --amend -m 'document the release' -m 'Sloprail-Cites-User: "+handbackPrompt+"'"),
	))
	if n := stopBlocks(e, proj, sess); n > blocks {
		t.Fatalf("the parent's cited commit of the patch was refused:\n%s", newBlocks(e, proj, sess, blocks))
	}
}

// T003_75: the root keeps the plain instruction (ask the user now): no hand-back.
// sr:proves subagents/citation-refusal-hands-the-change-back
func TestT003_75_TheRootIsToldToCiteNotToHandBack(t *testing.T) {
	e, proj, _ := project(t, citingRule)
	e.Run(proj, "s-003-75r", handbackPrompt, Turns("done", harness.CommitFile("c1", "docs/release.md", "the steps", "document the release")))
	got := strings.Join(e.BlockingErrorsFrom(proj, "s-003-75r", "Stop"), "\n")
	if !strings.Contains(got, "Sloprail-Cites-User: <exact quote>") || strings.Contains(got, "You are a sub-agent") {
		t.Fatalf("the root's refusal changed:\n%s", got)
	}
}

// T003_75: a stash is not a recorded ref. A sub-agent that stashes an uncited change passes its
// Stop; popping the stash and committing it is judged and refused.
func TestT003_75_AStashedUncitedChangeIsNotJudgedUntilItIsCommitted(t *testing.T) {
	e, proj, _ := project(t, citingRule)
	const sess = "s-003-75s"
	// The sub-agent first commits a file the rule does not match: a worktree left clean with no
	// commit is removed when the sub-agent ends (the stash lives in the repository's shared
	// refs/stash, not in the worktree), and this test needs the folder to still be there to
	// pop the stash in.
	sub := harness.SubagentScript(t, Turns("sub done",
		harness.CommitFile("c0", "notes/unrelated.txt", "not a doc", "an unrelated change"),
		Bash("w1", "mkdir -p docs && echo steps > docs/release.md"),
		Bash("w2", "git stash push -u -q"),
	))
	res := e.Run(proj, sess, handbackPrompt, Turns("root done", harness.Dispatch("d1", "write the docs", sub, harness.OwnTree(t))))
	if !e.NoSubagentStopBlock(proj, sess) {
		t.Fatalf("a sub-agent's stashed change was judged:\n%s", res.Output)
	}
	if got := stopRefusals(e, proj, sess); got != "" {
		t.Fatalf("the root was refused for a stash:\n%s", got)
	}
	if !harness.HasCap(t, harness.CapWorktrees) {
		// The stash is in the root's own tree and repository: popped and committed there, it is
		// judged at the root's Stop and refused.
		noSubagentFolder(t, e, proj, sess)
		if out := e.Git(proj, "stash", "list"); !strings.Contains(out, "stash@{0}") {
			t.Fatalf("premise: nothing was stashed: %q", out)
		}
		blocks := stopBlocks(e, proj, sess)
		applied := e.Run(proj, sess, "apply the stash", Turns("applied",
			Bash("a1", "git stash pop -q && git add docs/release.md"),
			Bash("a2", "git commit -q -m 'document the release'"),
		))
		if !applied.Saw("must cite") && !strings.Contains(newBlocks(e, proj, sess, blocks), "must cite") {
			t.Fatalf("the commit made from the popped stash was not judged:\n%s", applied.Output)
		}
		return
	}
	wt, agent := subagentFolder(t, e, proj, sess)
	if out := e.Git(wt, "stash", "list"); !strings.Contains(out, "stash@{0}") {
		t.Fatalf("premise: nothing was stashed: %q", out)
	}
	if r := subagentStopNow(t, e, proj, sess, wt, agent); harness.Blocked(r) {
		t.Fatalf("the sub-agent's Stop refused a stash:\n%s", r.Output)
	}

	runCommand(t, wt, "git stash pop -q && git add docs/release.md && git commit -q -m 'document the release'")
	r := subagentStopNow(t, e, proj, sess, wt, agent)
	if !harness.Blocked(r) || !strings.Contains(r.Output, "must cite") {
		t.Fatalf("the commit made from the popped stash was not judged:\n%s", r.Output)
	}
}

// T003_75b: a sub-agent that ONLY stashes an uncited docs/ change leaves its worktree clean with no
// commit, which Claude Code removes when the sub-agent ends; the stash stays in the repository's
// shared refs/stash. Nothing may break on the folder that is gone: the sub-agent's Stop passed
// (nothing recorded was uncited), the root's Stop neither errors nor refuses for the missing path,
// and the stash, popped in the main session and committed, is judged and refused.
func TestT003_75b_ASubagentThatOnlyStashesLosesItsWorktreeAndNothingBreaks(t *testing.T) {
	e, proj, _ := project(t, citingRule)
	const sess = "s-003-75b"
	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("w1", "mkdir -p docs && echo steps > docs/release.md"),
		Bash("w2", "git stash push -u -q"),
	))
	res := e.Run(proj, sess, handbackPrompt, Turns("root done", harness.Dispatch("d1", "write the docs", sub, harness.OwnTree(t))))
	if !e.NoSubagentStopBlock(proj, sess) {
		t.Fatalf("a sub-agent's stashed change was judged at its Stop:\n%s", res.Output)
	}
	if harness.HasCap(t, harness.CapWorktrees) {
		wt, _ := subagentFolder(t, e, proj, sess)
		if _, err := os.Stat(wt); err == nil {
			t.Fatalf("premise: the clean sub-agent's worktree %s was not removed", wt)
		}
	} else {
		// No worktree of its own, so none to lose: the root's tree is the one it stashed in.
		noSubagentFolder(t, e, proj, sess)
	}
	if out := e.Git(proj, "stash", "list"); !strings.Contains(out, "stash@{0}") {
		t.Fatalf("premise: the stash is not in the main repository: %q", out)
	}
	if got := stopRefusals(e, proj, sess); got != "" {
		t.Fatalf("the root was refused for a stash, or for the removed worktree:\n%s", got)
	}
	if r := e.StopNow(proj, sess, false); harness.Blocked(r) || r.Code != 0 {
		t.Fatalf("the root's Stop broke on the removed sub-agent worktree (exit %d):\n%s", r.Code, r.Output)
	}

	// Popped in the main session, then committed as its own command (the commit gate cannot check
	// a line that also runs the stash): judged and refused, at the commit or at the Stop.
	blocks := stopBlocks(e, proj, sess)
	applied := e.Run(proj, sess, "apply the stash", Turns("applied",
		Bash("a1", "git stash pop -q && git add docs/release.md"),
		Bash("a2", "git -C '"+proj+"' commit -q -m 'document the release'"),
	))
	if !applied.Saw("must cite") && !strings.Contains(newBlocks(e, proj, sess, blocks), "must cite") {
		t.Fatalf("the commit made from the popped stash was not refused with \"must cite\":\n%s", applied.Output)
	}
	if strings.Contains(e.Git(proj, "log", "--oneline"), "document the release") && !strings.Contains(newBlocks(e, proj, sess, blocks), "must cite") {
		t.Fatal("the uncited commit from the popped stash landed and its Stop did not refuse it")
	}
}
