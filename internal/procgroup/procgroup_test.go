package procgroup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gone(pgid int) bool { return syscall.Kill(-pgid, 0) != nil }

// KillAll ends a registered group, grandchildren included, and an unregistered one is left alone.
func TestKillAllEndsRegisteredGroupsOnly(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "grandchild")
	tracked := exec.Command("sh", "-c", "sleep 60 & echo $! > "+pidFile+"; wait")
	untrack, err := Start(tracked, true)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return len(readPids(pidFile)) == 1 }, 30*time.Second, 10*time.Millisecond)
	other := exec.Command("sleep", "60")
	Own(other)
	require.NoError(t, other.Start())
	t.Cleanup(func() { _ = syscall.Kill(-other.Process.Pid, syscall.SIGKILL); _ = other.Wait() })
	reaped := make(chan struct{})
	go func() { _ = tracked.Wait(); close(reaped) }()
	assert.Equal(t, 1, Tracked())

	KillAll(3 * time.Second)
	<-reaped
	untrack()

	assert.Eventually(t, func() bool { return dead(readPids(pidFile)[0]) }, 30*time.Second, 20*time.Millisecond, "the tracked group's grandchild survived")
	assert.False(t, gone(other.Process.Pid), "an unregistered group is not touched")
	assert.Equal(t, 0, Tracked())
}

// A group that ignores SIGTERM is killed once the grace has passed.
func TestKillAllEscalatesToSIGKILL(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	// The pids are written after the trap is set, so seeing both means SIGTERM is already ignored.
	c := exec.Command("sh", "-c", "trap '' TERM; echo $$ > "+pidFile+"; sleep 120 & echo $! >> "+pidFile+"; while :; do wait; done")
	untrack, err := Start(c, true)
	require.NoError(t, err)
	defer untrack()
	reaped := make(chan struct{})
	go func() { _ = c.Wait(); close(reaped) }()
	require.Eventually(t, func() bool { return len(readPids(pidFile)) == 2 }, 30*time.Second, 10*time.Millisecond)

	start := time.Now()
	KillAll(500 * time.Millisecond)
	<-reaped
	assert.GreaterOrEqual(t, time.Since(start), 400*time.Millisecond, "SIGTERM was given its grace first")
	// Leader and its child, judged by state: an orphan killed under an init that does not reap is
	// a zombie, which still answers kill(-pgid, 0).
	assert.Eventually(t, func() bool {
		for _, p := range readPids(pidFile) {
			if !dead(p) {
				return false
			}
		}
		return true
	}, 30*time.Second, 20*time.Millisecond, "the group survived the SIGKILL")
}

// A child that is not a group leader (stdin is a terminal) is signalled directly.
func TestUngroupedChildIsSignalledByPid(t *testing.T) {
	c := exec.Command("sleep", "60")
	untrack, err := Start(c, false)
	require.NoError(t, err)
	defer untrack()
	reaped := make(chan struct{})
	go func() { _ = c.Wait(); close(reaped) }()
	KillAll(2 * time.Second)
	select {
	case <-reaped:
	case <-time.After(5 * time.Second):
		t.Fatal("the ungrouped child was not signalled")
	}
}

// The helper process: handles signals, starts the children named by SR_PG_CMDS, reports pids.
func TestMain(m *testing.M) {
	if out := os.Getenv("SR_PG_PIDS"); out != "" {
		helper(out)
		return
	}
	os.Exit(m.Run())
}

func helper(out string) {
	ExitOnSignal(nil)
	pidFile := out + ".pids"
	script := "echo $$ >> " + pidFile + "; sleep 120 & echo $! >> " + pidFile + "; wait"
	for i := 0; i < 2; i++ {
		c := exec.Command("sh", "-c", script)
		if i == 1 && os.Getenv("SR_PG_ADOPT") != "" { // started by other means (a pty), then adopted
			Own(c)
			if c.Start() != nil {
				os.Exit(3)
			}
			if _, err := Adopt(c, true); err != nil {
				os.Exit(3)
			}
		} else if _, err := Start(c, true); err != nil {
			os.Exit(3)
		}
		go func() { _ = c.Wait() }() // reaped here, so a killed leader is not left a zombie of ours
	}
	// Ready once both groups have reported their leader and their grandchild.
	for len(readPids(pidFile)) < 4 {
		time.Sleep(10 * time.Millisecond)
	}
	_ = os.WriteFile(out+".tmp", nil, 0o644)
	_ = os.Rename(out+".tmp", out)
	if os.Getenv("SR_PG_LATE") != "" {
		// A start racing the exit: once the handler has closed registration, a start fails.
		for {
			c := exec.Command("sleep", "120")
			if _, err := Start(c, true); err != nil {
				_ = os.WriteFile(out+".late", []byte(err.Error()), 0o644)
				select {} // the handler is exiting the process
			}
			_ = KillGroup(c.Process.Pid)
			_ = c.Wait()
		}
	}
	select {} // until the handler ends the process
}

func readPids(file string) []int {
	b, _ := os.ReadFile(file)
	var pids []int
	for _, f := range strings.Fields(string(b)) {
		if n, err := strconv.Atoi(f); err == nil {
			pids = append(pids, n)
		}
	}
	return pids
}

// dead reports whether pid is gone or a zombie: a killed process nobody has reaped (an orphan under
// a container's init, which may not reap) still answers kill(pid, 0), but runs nothing.
func dead(pid int) bool {
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return true
	}
	st := strings.TrimSpace(string(out))
	return st == "" || st[0] == 'Z'
}

func runHelper(t *testing.T, sig syscall.Signal, late bool, env ...string) (code int, pids []int, lateErr string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(append(os.Environ(), env...), "SR_PG_PIDS="+out)
	if late {
		cmd.Env = append(cmd.Env, "SR_PG_LATE=1")
	}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	require.Eventually(t, func() bool { _, err := os.Stat(out); return err == nil }, 30*time.Second, 10*time.Millisecond)
	pids = readPids(out + ".pids")
	require.Len(t, pids, 4)
	require.NoError(t, cmd.Process.Signal(sig))
	err := cmd.Wait()
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	// Every process of every group is dead (or an unreaped zombie), whatever the runner's init does.
	require.Eventually(t, func() bool {
		for _, p := range pids {
			if !dead(p) {
				return false
			}
		}
		return true
	}, 30*time.Second, 20*time.Millisecond, "a group process survived %s: %v", sig, pids)
	l, _ := os.ReadFile(out + ".late")
	return code, pids, string(l)
}

// SIGTERM and SIGINT end every registered group before the process exits 128+signal.
func TestExitOnSignalEndsGroups(t *testing.T) {
	for sig, want := range map[syscall.Signal]int{syscall.SIGTERM: 143, syscall.SIGINT: 130} {
		code, _, _ := runHelper(t, sig, false)
		assert.Equal(t, want, code, sig.String())
	}
}

// A child started by other means and adopted (sr-eval's agent on a pty) ends with the rest:
// a stopped eval run left its simulated user running, and billing, with nobody reading it.
func TestAdoptedChildEndsOnSignal(t *testing.T) {
	code, _, _ := runHelper(t, syscall.SIGTERM, false, "SR_PG_ADOPT=1")
	assert.Equal(t, 143, code)
}

// A child started while the exit is under way is refused, never left unregistered and orphaned.
func TestStartAfterTheSignalFails(t *testing.T) {
	code, _, late := runHelper(t, syscall.SIGTERM, true)
	assert.Equal(t, 143, code)
	assert.Contains(t, late, "exiting")
}

// A target waited for since the snapshot is not signalled: its pid may be someone else's now.
func TestTerminateSkipsTargetsNoLongerRegistered(t *testing.T) {
	c := exec.Command("sleep", "60")
	Own(c)
	require.NoError(t, c.Start())
	t.Cleanup(func() { _ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL); _ = c.Wait() })
	terminate([]target{{pid: c.Process.Pid, group: true}}, 100*time.Millisecond) // never registered
	assert.False(t, gone(c.Process.Pid), "an unregistered target was signalled")
}
