package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A judge outage (the harness behind sr-agent dies: a usage limit, a bad login) is no verdict
// on the work. `sr-checks run` says so once, with the cause, instead of refusing every judged
// rule with an identical "could not be evaluated"; it asks each judge a second time first; and
// it stores nothing, so the next run judges afresh.

// usageLimitClaude counts its calls in $LEDGER and dies the way claude does at a usage limit:
// the message on stdout, status 1.
const usageLimitClaude = `#!/bin/sh
echo call >>"$LEDGER"
echo "Claude AI usage limit reached|1760000000"
exit 1
`

// T001_32: three judged rules, one outage: one "judges unavailable" refusal naming the cause,
// each judge tried twice, and a later run with a working judge passes (nothing was cached).
func TestT001_32_AJudgeOutageIsReportedOnceWithItsCause(t *testing.T) {
	e, proj := session(t)
	ledger := filepath.Join(t.TempDir(), "ledger")
	for _, name := range []string{"alpha", "beta", "gamma"} {
		e.FileGuard(proj, name, judgeRule, map[string]string{"rubric.md.j2": rubric + name + "\n"})
	}
	base := e.CommitAll(proj, "the rules")
	e.WriteFile(proj, "docs/a.md", "the release is Friday\n")
	e.CommitAll(proj, "add a")
	e.InstallShim("claude", usageLimitClaude)
	env := append(e.SessionEnv(sessionID), "LEDGER="+ledger, "SLOPRAIL_JUDGE_RETRY_BACKOFF=0s")
	run := func() (int, string) {
		r := quiet(e.CLIDirectEnv(proj, env, "sr-checks", "run", "--base", base, "--head", "HEAD"))
		return r.Code, r.Output
	}

	code, out := run()
	if code == 0 {
		t.Fatalf("an outage must stay fail-closed:\n%s", out)
	}
	contains(t, out, "judges unavailable: 3 rules not evaluated: usage limit", "alpha", "beta", "gamma")
	if n := strings.Count(out, "could not be evaluated"); n != 1 {
		t.Fatalf("the outage was reported %d times, want once:\n%s", n, out)
	}
	if b, _ := os.ReadFile(ledger); strings.Count(string(b), "call\n") != 6 {
		t.Fatalf("each judge must be tried twice (3 rules x 2), ledger:\n%s", b)
	}

	// Nothing was stored as a verdict: a working judge is asked again and passes.
	e.InstallJudgeClaudeCapturing(proj, promptFile, verdictPass)
	if code, out = run(); code != 0 {
		t.Fatalf("the outage was cached as a verdict:\n%s", out)
	}
}
