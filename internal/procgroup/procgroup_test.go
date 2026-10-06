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
	tracked := exec.Command("sh", "-c", "sleep 60 & wait")
	untrack, err := Start(tracked, true)
	require.NoError(t, err)
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

	assert.True(t, gone(tracked.Process.Pid), "the tracked group, its sleep included, is gone")
	assert.False(t, gone(other.Process.Pid), "an unregistered group is not touched")
	assert.Equal(t, 0, Tracked())
}

// A group that ignores SIGTERM is killed once the grace has passed.
func TestKillAllEscalatesToSIGKILL(t *testing.T) {
	c := exec.Command("sh", "-c", "trap '' TERM; while :; do sleep 1; done")
	untrack, err := Start(c, true)
	require.NoError(t, err)
	defer untrack()
	reaped := make(chan struct{})
	go func() { _ = c.Wait(); close(reaped) }()
	time.Sleep(200 * time.Millisecond) // the trap is installed

	start := time.Now()
	KillAll(500 * time.Millisecond)
	<-reaped
	assert.True(t, gone(c.Process.Pid))
	assert.GreaterOrEqual(t, time.Since(start), 400*time.Millisecond, "SIGTERM was given its grace first")
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
	var pids []string
	for i := 0; i < 2; i++ {
		c := exec.Command("sh", "-c", "sleep 120 & wait")
		if _, err := Start(c, true); err != nil {
			os.Exit(3)
		}
		pids = append(pids, strconv.Itoa(c.Process.Pid))
	}
	_ = os.WriteFile(out+".tmp", []byte(strings.Join(pids, "\n")), 0o644)
	_ = os.Rename(out+".tmp", out)
	if os.Getenv("SR_PG_LATE") != "" {
		// A start racing the exit: once the handler has closed registration, a start fails.
		for {
			c := exec.Command("sleep", "120")
			if _, err := Start(c, true); err != nil {
				_ = os.WriteFile(out+".late", []byte(err.Error()), 0o644)
				select {} // the handler is exiting the process
			}
			pid := c.Process.Pid
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			_ = c.Wait()
		}
	}
	time.Sleep(time.Minute)
}

func runHelper(t *testing.T, sig syscall.Signal, late bool) (code int, pids []int, lateErr string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "pids")
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "SR_PG_PIDS="+out)
	if late {
		cmd.Env = append(cmd.Env, "SR_PG_LATE=1")
	}
	require.NoError(t, cmd.Start())
	require.Eventually(t, func() bool { _, err := os.Stat(out); return err == nil }, 20*time.Second, 20*time.Millisecond)
	b, _ := os.ReadFile(out)
	for _, f := range strings.Fields(string(b)) {
		n, _ := strconv.Atoi(f)
		pids = append(pids, n)
	}
	require.NoError(t, cmd.Process.Signal(sig))
	err := cmd.Wait()
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	l, _ := os.ReadFile(out + ".late")
	return code, pids, string(l)
}

// SIGTERM and SIGINT end every registered group before the process exits 128+signal.
func TestExitOnSignalEndsGroups(t *testing.T) {
	for sig, want := range map[syscall.Signal]int{syscall.SIGTERM: 143, syscall.SIGINT: 130} {
		code, pids, _ := runHelper(t, sig, false)
		assert.Equal(t, want, code, sig.String())
		require.Len(t, pids, 2)
		for _, p := range pids {
			assert.True(t, gone(p), "group %d survived %s", p, sig)
		}
	}
}

// A child started while the exit is under way is refused, never left unregistered and orphaned.
func TestStartAfterTheSignalFails(t *testing.T) {
	code, pids, late := runHelper(t, syscall.SIGTERM, true)
	assert.Equal(t, 143, code)
	assert.Contains(t, late, "exiting")
	for _, p := range pids {
		assert.True(t, gone(p))
	}
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
