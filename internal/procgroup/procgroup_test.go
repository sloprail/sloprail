package procgroup

import (
	"os/exec"
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
	Own(tracked)
	require.NoError(t, tracked.Start())
	untrack := Track(tracked)
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
	Own(c)
	require.NoError(t, c.Start())
	defer Track(c)()
	reaped := make(chan struct{})
	go func() { _ = c.Wait(); close(reaped) }()
	time.Sleep(200 * time.Millisecond) // the trap is installed

	start := time.Now()
	KillAll(500 * time.Millisecond)
	<-reaped
	assert.True(t, gone(c.Process.Pid))
	assert.GreaterOrEqual(t, time.Since(start), 400*time.Millisecond, "SIGTERM was given its grace first")
}
