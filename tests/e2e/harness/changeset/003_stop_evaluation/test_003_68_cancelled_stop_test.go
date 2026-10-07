package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

const slowRubric = "RULE=docs\n{{ change }}\n"

// T003_68: a `sr-checks run` killed while the judge is still thinking (the user interrupted, the
// harness timed out, the machine slept) decided nothing. It stores no pass: the next Stop reads
// the range as not judged, the next run asks the judge again, and a judge that refuses refuses.
// sr:proves cache/unfinished-never-stored
func TestT003_68_AStopKilledMidJudgeIsNeverAPass(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.CommitAll(proj, "the project")
	e.FileGuard(proj, "docs", judgeRule, map[string]string{"rubric.md.j2": slowRubric})
	e.CommitAll(proj, "the judged rule")
	const sess = "s-003-68"
	e.Run(proj, sess, "begin", Turns("done", harness.CommitFile("c0", "docs/clean.md", "clean words", "add clean")))

	log := filepath.Join(t.TempDir(), "judge.log")
	e.InstallJudgeClaudeSlow(log, 4)
	e.WriteFile(proj, "docs/bad.md", "VERDICT-FAIL words\n")
	e.CommitAll(proj, "add bad")

	stop := e.CheckRunCmd(proj, sess, "origin/main", "HEAD")
	stop.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := stop.Start(); err != nil {
		t.Fatalf("start the run: %v", err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		if body, _ := os.ReadFile(log); strings.Contains(string(body), "docs") {
			break
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(-stop.Process.Pid, syscall.SIGKILL)
			t.Fatal("the judge was never asked")
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(-stop.Process.Pid, syscall.SIGKILL) // cancelled while the judge is thinking
	_ = stop.Wait()

	if v := e.CheckVerify(proj, sess, "origin/main", "HEAD"); v.Code == 0 || !strings.Contains(v.Output, "not judged yet") {
		t.Fatalf("the cancelled run should have left the range unjudged, not passed:\n%s", v.Output)
	}

	e.CheckRunRaw(proj, sess, "origin/main", "HEAD")
	r := e.StopNow(proj, sess, false)
	if !harness.Blocked(r) || !strings.Contains(r.Output, "JUDGE-NO-docs") {
		t.Fatalf("the next run treated the cancelled judge as a pass instead of asking again:\n%s", r.Output)
	}
	body, _ := os.ReadFile(log)
	if n := strings.Count(strings.TrimSpace(string(body)), "\n") + 1; n < 2 {
		t.Fatalf("the judge was asked %d time(s); the cancelled one must be asked again:\n%s", n, body)
	}
}
