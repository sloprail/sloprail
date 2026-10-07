package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// A judge that returns no verdict, and a check that was skipped, are no verdict on the content:
// `sr-checks run` stores neither as a pass, so the next run asks again. A verdict's key is the
// content alone: another session, or another branch over the same content, reads the same one.

// T001_37: a judge whose output cannot be read as a verdict fails the run, twice in a row: the
// judge is asked again each time, and once it answers, the content passes (nothing was stored).
// sr:proves cache/unfinished-never-stored
func TestT001_37_ANoVerdictJudgeIsAskedAgain(t *testing.T) {
	e, proj := session(t)
	base := judged(t, e, proj, "this is not a verdict")
	env := append(e.SessionEnv(sessionID), "SLOPRAIL_JUDGE_RETRY_BACKOFF=0s")
	run := func() (int, string) {
		r := quiet(e.CLIDirectEnv(proj, env, "sr-checks", "run", "--base", base, "--head", "HEAD"))
		return r.Code, r.Output
	}

	code, out := run()
	if code == 0 {
		t.Fatalf("a judge with no verdict must stay fail-closed:\n%s", out)
	}
	first := e.JudgeCalls(proj, promptFile, "")
	if first < 1 {
		t.Fatalf("the judge was never asked:\n%s", out)
	}
	if code, out = run(); code == 0 {
		t.Fatalf("a judge with no verdict must stay fail-closed on the second run:\n%s", out)
	}
	if second := e.JudgeCalls(proj, promptFile, ""); second <= first {
		t.Fatalf("the second run did not ask the judge again (%d calls after the first run, %d after the second): no verdict was stored", first, second)
	}

	e.InstallJudgeClaudeCapturing(proj, promptFile, verdictPass)
	if code, out = run(); code != 0 {
		t.Fatalf("the content was not judged afresh once the judge answered:\n%s", out)
	}
}

// refusesUntilFixed refuses, with a cheap script, until $FIXED exists.
const refusesUntilFixed = `#!/bin/sh
cat >/dev/null
if [ ! -e "$FIXED" ]; then
  echo '{"reason": "not yet"}'
  exit 1
fi
exit 0
`

// T001_38: a judge the rule's own cheap check kept from running is skipped, and a skip is no
// pass: once the cheap check passes, the judge is asked, for the first time.
// sr:proves cache/unfinished-never-stored
func TestT001_38_ASkippedJudgeIsAskedOnceItsTurnComes(t *testing.T) {
	e, proj := session(t)
	fixed := filepath.Join(t.TempDir(), "fixed")
	e.FileGuard(proj, "docs", "match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n  - judge: ./rubric.md.j2\n",
		map[string]string{"check.sh": refusesUntilFixed, "rubric.md.j2": rubric})
	base := e.CommitAll(proj, "the rule")
	e.WriteFile(proj, "docs/a.md", "the release is Friday\n")
	e.CommitAll(proj, "add a")
	e.InstallJudgeClaudeCapturing(proj, promptFile, verdictPass)
	env := append(e.SessionEnv(sessionID), "FIXED="+fixed)
	run := func() (int, string) {
		r := quiet(e.CLIDirectEnv(proj, env, "sr-checks", "run", "--base", base, "--head", "HEAD"))
		return r.Code, r.Output
	}

	if code, out := run(); code == 0 {
		t.Fatalf("the cheap check's refusal must refuse:\n%s", out)
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n != 0 {
		t.Fatalf("the judge ran %d times behind a refusing cheap check, want 0", n)
	}
	if err := os.WriteFile(fixed, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := run(); code != 0 {
		t.Fatalf("the rule must pass once its cheap check does:\n%s", out)
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n != 1 {
		t.Fatalf("the judge was asked %d times, want 1: the skip was read as a verdict", n)
	}
}

// T001_39: the same content judged by another session, from another branch, is a cache hit: the
// session, the agent and the branch are not part of a verdict's key.
// sr:proves cache/verdict-identity
func TestT001_39_AnotherSessionAndBranchReadTheSameVerdict(t *testing.T) {
	e, proj := session(t)
	base := judged(t, e, proj, verdictPass)
	run := func(env []string) {
		t.Helper()
		if r := quiet(e.CLIDirectEnv(proj, env, "sr-checks", "run", "--base", base, "--head", "HEAD")); r.Code != 0 {
			t.Fatalf("run: exit %d:\n%s", r.Code, r.Output)
		}
	}

	run(e.SessionEnv(sessionID))
	if n := e.JudgeCalls(proj, promptFile, ""); n != 1 {
		t.Fatalf("premise: judged %d times, want 1", n)
	}
	e.Git(proj, "checkout", "-q", "-b", "another-branch")
	run(e.SessionEnv("another-session"))
	if n := e.JudgeCalls(proj, promptFile, ""); n != 1 {
		t.Fatalf("another session on another branch judged the same content again: %d calls, want 1", n)
	}
}
