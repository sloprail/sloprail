package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A check that ERRORED (it could not run, or said so) is no verdict on the content: `sr-checks
// run` refuses (fail-closed) but stores nothing under the key, so the next run over the same
// content tries again. A check's own refusal is a verdict: stored, replayed without running.

// errorsOnce counts its runs in $LEDGER, says {"error": true} until $FIXED exists, then passes.
const errorsOnce = `#!/bin/sh
cat >/dev/null
echo run >>"$LEDGER"
if [ ! -e "$FIXED" ]; then
  echo '{"reason": "sr-test could not run", "error": true}'
  exit 1
fi
exit 0
`

const refusesAlways = `#!/bin/sh
cat >/dev/null
echo run >>"$LEDGER"
echo '{"reason": "forbidden words"}'
exit 1
`

func runCount(t *testing.T, ledger string) int {
	t.Helper()
	b, err := os.ReadFile(ledger)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "run\n")
}

// T001_30: an error is refused, not cached; once the check works, the same content is judged afresh.
// sr:proves cache/unfinished-never-stored
func TestT001_30_AnErroredCheckIsRetriedNotReplayed(t *testing.T) {
	e, proj := session(t)
	dir := t.TempDir()
	ledger, fixed := filepath.Join(dir, "ledger"), filepath.Join(dir, "fixed")
	e.FileGuard(proj, "docs", "match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n", map[string]string{"check.sh": errorsOnce})
	base := e.CommitAll(proj, "the rule")
	e.WriteFile(proj, "docs/a.md", "hello\n")
	e.CommitAll(proj, "add a")
	env := append(e.SessionEnv(sessionID), "LEDGER="+ledger, "FIXED="+fixed)
	run := func() (code int, out string) {
		r := quiet(e.CLIDirectEnv(proj, env, "sr-checks", "run", "--base", base, "--head", "HEAD"))
		return r.Code, r.Output
	}

	code, out := run()
	if code == 0 {
		t.Fatalf("an errored check must stay fail-closed:\n%s", out)
	}
	if n := runCount(t, ledger); n != 1 {
		t.Fatalf("runs = %d, want 1", n)
	}

	if err := os.WriteFile(fixed, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out = run()
	if code != 0 {
		t.Fatalf("the same content must be judged again once the check works (the error was cached):\n%s", out)
	}
	if n := runCount(t, ledger); n != 2 {
		t.Fatalf("runs = %d, want 2: the second run did not run the check", n)
	}
}

// T001_31: a script's refusal is stored (verify says why) but asked again by the next run: a
// script is cheap and may read what is not in the key. A judge's refusal is replayed instead
// (T001_31b).
func TestT001_31_AScriptRefusalIsAskedAgain(t *testing.T) {
	e, proj := session(t)
	ledger := filepath.Join(t.TempDir(), "ledger")
	e.FileGuard(proj, "docs", "match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n", map[string]string{"check.sh": refusesAlways})
	base := e.CommitAll(proj, "the rule")
	e.WriteFile(proj, "docs/a.md", "hello\n")
	e.CommitAll(proj, "add a")
	env := append(e.SessionEnv(sessionID), "LEDGER="+ledger)
	for i := 0; i < 2; i++ {
		r := quiet(e.CLIDirectEnv(proj, env, "sr-checks", "run", "--base", base, "--head", "HEAD"))
		if r.Code == 0 {
			t.Fatalf("a refusal must refuse:\n%s", r.Output)
		}
	}
	if n := runCount(t, ledger); n != 2 {
		t.Fatalf("runs = %d, want 2: a script refusal is asked again", n)
	}
}

// T001_31b: a judge's refusal is replayed: repeated runs over still-failing content never pay
// for the judge again.
func TestT001_31b_AJudgeRefusalIsReplayedWithoutAskingTheJudge(t *testing.T) {
	e, proj := session(t)
	base := judged(t, e, proj, verdictFail)
	for i := 0; i < 3; i++ {
		r := checks(e, proj, "run", "--base", base, "--head", "HEAD")
		if r.Code != 1 {
			t.Fatalf("run %d: exit %d, want the judge's refusal:\n%s", i, r.Code, r.Output)
		}
		contains(t, r.Output, "the ADR is not cited")
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n != 1 {
		t.Fatalf("the judge was asked %d times over three runs of the same content, want 1", n)
	}
}
