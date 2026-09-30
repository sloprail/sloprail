package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// crashOnce is a check that runs twice in the test: the first time it kills the
// evaluation itself (its parent, sr-session) the way a crash or a kill would, after the run has
// been recorded RUNNING and before it can be finished; the second time it passes.
func crashOnce(ledger, flag string) string {
	return `#!/bin/sh
cat >/dev/null
echo run >> '` + ledger + `'
if [ ! -e '` + flag + `' ]; then
  : > '` + flag + `'
  kill -9 $PPID
  sleep 30
fi
exit 0
`
}

// T003_15: an evaluation that dies half-way leaves its run RUNNING, and a run that
// never finished is no watermark. The next Stop has no pass to start from — it
// judges the range again — rather than finding it already passed and empty.
func TestT003_15_ACrashThatLeftARunUnfinishedIsNotAWatermark(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	dir := t.TempDir()
	led, flag := filepath.Join(dir, "ledger"), filepath.Join(dir, "crashed")
	e.FileGuard(proj, "docs", docsRule, map[string]string{"check.sh": crashOnce(led, flag)})
	e.CommitSeedThenRules(proj, "the project")
	e.Run(proj, "s-003-15", "write the doc", Turns("done", harness.CommitFile("c1", "docs/a.md", "clean words\n", "add a")))

	// The Stop the session itself ran was the one that crashed: the check started
	// and never finished, and the run is recorded but not complete.
	if n := strings.Count(readLedger(t, led), "run"); n != 1 {
		t.Fatalf("the check should have run once and crashed the evaluation, ran %d times", n)
	}
	state := e.ChecksSQL(proj, "s-003-15", "select json_extract(metadata, '$.state') as state from check_runs where check_id = 'file-guard/docs'")
	if !strings.Contains(state.Output, "running") {
		t.Fatalf("the crashed evaluation should have left a run recorded as running:\n%s", state.Output)
	}

	// The next Stop, with nothing new committed: had the crashed run counted as a
	// pass at this head, the range would be empty and no check would run.
	r := e.StopNow(proj, "s-003-15", false)
	if n := strings.Count(readLedger(t, led), "run"); n != 2 {
		t.Fatalf("the range was not judged again after the crash (check ran %d times in all):\n%s", n, r.Output)
	}
	if harness.Blocked(r) {
		t.Fatalf("the second evaluation should pass:\n%s", r.Output)
	}
}

func readLedger(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
