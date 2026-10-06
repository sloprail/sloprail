package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Killing `sr-checks run` must not orphan what it started (#273). Check scripts and judge
// shells run in process groups of their own, so a signal to the parent never reached them: a
// script outlived its run by minutes. SIGTERM now ends every child group before the run exits.

// pidsOf reads the pids a child wrote, one per line, waiting until want of them exist.
func waitPids(t *testing.T, file string, want int) []int {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(file)
		var pids []int
		for _, f := range strings.Fields(string(b)) {
			if n, err := strconv.Atoi(f); err == nil {
				pids = append(pids, n)
			}
		}
		if len(pids) >= want {
			return pids
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s never held %d pids", file, want)
	return nil
}

// running reports whether pid runs: a killed orphan nobody reaps (a container's init may not) is a
// zombie, which still answers kill(pid, 0) but runs nothing.
func alive(pid int) bool {
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	st := strings.TrimSpace(string(out))
	return st != "" && st[0] != 'Z'
}

func startRun(t *testing.T, e *Env, proj, base string, extra ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(e.BinPath("sr-checks"), "run", "--base", base, "--head", "HEAD")
	cmd.Dir = proj
	env := append(harness.HostEnv(), "HOME="+e.HomeDir(), "SLOP_SUBBIN_DIR="+e.BinDir())
	env = append(env, e.SessionEnv(sessionID)...)
	cmd.Env = append(env, extra...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func killAndExpectNoChildren(t *testing.T, cmd *exec.Cmd, pids []int) {
	t.Helper()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("sr-checks did not exit after SIGTERM")
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		left := false
		for _, p := range pids {
			left = left || alive(p)
		}
		if !left {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, p := range pids {
		if alive(p) {
			_ = syscall.Kill(-p, syscall.SIGKILL) // only our own children, by group
			_ = syscall.Kill(p, syscall.SIGKILL)
			t.Errorf("pid %d outlived the SIGTERM of sr-checks", p)
		}
	}
}

const sleepyScript = `#!/bin/sh
cat >/dev/null
echo $$ >>"$PIDS"
sleep 120 &
echo $! >>"$PIDS"
wait
`

// T001_33: SIGTERM to `sr-checks run` leaves no check script and no child of it running.
func TestT001_33_SIGTERMLeavesNoCheckScriptRunning(t *testing.T) {
	e, proj := session(t)
	pids := filepath.Join(t.TempDir(), "pids")
	e.FileGuard(proj, "slow", "match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n", map[string]string{"check.sh": sleepyScript})
	base := e.CommitAll(proj, "the rule")
	e.WriteFile(proj, "docs/a.md", "hello\n")
	e.CommitAll(proj, "add a")

	cmd := startRun(t, e, proj, base, "PIDS="+pids)
	running := waitPids(t, pids, 2)
	killAndExpectNoChildren(t, cmd, running)
}

// sleepyClaude stands in for the model: it records its pid and its child's, and never answers.
const sleepyClaude = `#!/bin/sh
echo $$ >>"$PIDS"
sleep 120 &
echo $! >>"$PIDS"
wait
`

// T001_34: the same for a judge: the model call sr-agent started goes down with the run.
func TestT001_34_SIGTERMLeavesNoJudgeModelCallRunning(t *testing.T) {
	e, proj := session(t)
	pids := filepath.Join(t.TempDir(), "pids")
	e.FileGuard(proj, "docs", judgeRule, map[string]string{"rubric.md.j2": rubric})
	base := e.CommitAll(proj, "the rule")
	e.WriteFile(proj, "docs/a.md", "the release is Friday\n")
	e.CommitAll(proj, "add a")
	e.InstallShim("claude", sleepyClaude)

	cmd := startRun(t, e, proj, base, "PIDS="+pids)
	running := waitPids(t, pids, 2)
	killAndExpectNoChildren(t, cmd, running)
}
