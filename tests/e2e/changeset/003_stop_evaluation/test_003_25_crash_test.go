package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// crashOnce is a check that runs twice in the test: the first time it kills the
// evaluation itself (its parent, sr-checks) the way a crash or a kill would, after the run has
// been recorded RUNNING and before it can be finished; the second time it passes.
func crashOnce(ledger, flag string) string {
	return `#!/bin/sh
cat >/dev/null
echo run >> '` + ledger + `'
if [ ! -e '` + flag + `' ]; then
  : > '` + flag + `'
  # The evaluation is the ancestor named sr-checks. $PPID is it only where sh
  # execs a single command (bash, as /bin/sh on macOS); under dash it is the
  # wrapper shell, and killing that makes the check fail instead of crashing the
  # evaluation. So walk up to sr-checks.
  p=$PPID
  while [ -n "$p" ] && [ "$(basename "$(ps -o comm= -p "$p" | tr -d ' ')")" != "sr-checks" ]; do
    p="$(ps -o ppid= -p "$p" | tr -d ' ')"
  done
  kill -9 "$p"
  sleep 30
fi
exit 0
`
}

// T003_25: an evaluation that dies half-way (the `sr-checks run` the agent asked for is killed
// while a check runs) leaves nothing stored: a run that never finished is no pass. The next Stop
// has no result to start from — it checks the range again — rather than finding it already
// passed and empty.
func TestT003_25_ACrashThatLeftARunUnfinishedIsNotAWatermark(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	dir := t.TempDir()
	led, flag := filepath.Join(dir, "ledger"), filepath.Join(dir, "crashed")
	e.FileGuard(proj, "docs", docsRule, map[string]string{"check.sh": crashOnce(led, flag)})
	e.CommitSeedThenRules(proj, "the project")
	e.Run(proj, "s-003-25", "write the doc", Turns("done", harness.CommitFile("c1", "docs/a.md", "clean words\n", "add a")))

	// The `sr-checks run` the session itself asked for was the one that crashed: the check
	// started and never finished (a script is re-run by the Stop's own verification, once).
	if n := strings.Count(readLedger(t, led), "run"); n != 2 {
		t.Fatalf("the check should have run once and crashed the evaluation, then once at the Stop; ran %d times", n)
	}
	for _, run := range e.CacheRecords(proj) {
		if run.Complete {
			t.Fatalf("the crashed evaluation should have left no finished run behind: %+v", run)
		}
	}

	// The next Stop, with nothing new committed: had the crashed run counted as a
	// pass at this head, the range would be empty and no check would run.
	r := e.StopNow(proj, "s-003-25", false)
	if n := strings.Count(readLedger(t, led), "run"); n != 3 {
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
