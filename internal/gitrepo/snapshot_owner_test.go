package gitrepo

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

func registered(t *testing.T, dir, path string) bool {
	out, err := run(dir, "worktree", "list", "--porcelain")
	require.NoError(t, err)
	return strings.Contains(out, "worktree "+path+"\n") || strings.Contains(out, "worktree "+evalPath(path)+"\n")
}

func evalPath(p string) string {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return r
}

func deadPID(t *testing.T) int {
	cmd := exec.Command("true")
	require.NoError(t, cmd.Run())
	return cmd.Process.Pid
}

func TestSnapshot_DeadOwnerSweptLiveKept(t *testing.T) {
	dir := initRepo(t)
	head := commit(t, dir, "a.txt", "x")
	parent := t.TempDir()
	dead, err := AddSnapshot(dir, parent, head)
	require.NoError(t, err)
	live, err := AddSnapshot(dir, parent, head)
	require.NoError(t, err)
	t.Cleanup(func() { live.Remove() })
	// the first snapshot's owner "dies"
	untrack(dead)
	require.NoError(t, os.WriteFile(filepath.Join(dead.root, ownerFile), []byte(itoa(deadPID(t))+"\nMon Jan  1 00:00:00 2001\n"), 0o644))
	assert.True(t, IsSnapshot(dead.Path))
	assert.True(t, IsSnapshot(filepath.Join(dead.Path, "sub")))
	assert.False(t, IsSnapshot(dir))

	third, err := AddSnapshot(dir, parent, head) // sweeps
	require.NoError(t, err)
	t.Cleanup(func() { third.Remove() })
	_, statErr := os.Stat(dead.root)
	assert.True(t, os.IsNotExist(statErr), "dead owner's snapshot removed")
	assert.False(t, registered(t, dir, dead.Path))
	assert.DirExists(t, live.Path)
	assert.True(t, registered(t, dir, live.Path))
}

func itoa(i int) string { return strconv.Itoa(i) }

func TestSnapshot_SIGTERMLeavesNoRegistration(t *testing.T) {
	if os.Getenv("SR_SNAP_CHILD") != "" {
		dir, head := os.Getenv("SR_SNAP_DIR"), os.Getenv("SR_SNAP_HEAD")
		stop := CleanupOnSignal()
		defer stop()
		s, err := AddSnapshot(dir, os.Getenv("SR_SNAP_PARENT"), head)
		if err != nil {
			os.Exit(3)
		}
		_ = os.WriteFile(os.Getenv("SR_SNAP_READY"), []byte(s.Path), 0o644)
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	dir := initRepo(t)
	head := commit(t, dir, "a.txt", "x")
	parent, ready := t.TempDir(), filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestSnapshot_SIGTERMLeavesNoRegistration$")
	cmd.Env = append(os.Environ(), "SR_SNAP_CHILD=1", "SR_SNAP_DIR="+dir, "SR_SNAP_HEAD="+head, "SR_SNAP_PARENT="+parent, "SR_SNAP_READY="+ready)
	require.NoError(t, cmd.Start())
	var path string
	require.Eventually(t, func() bool { b, err := os.ReadFile(ready); path = string(b); return err == nil }, 20*time.Second, 50*time.Millisecond)
	require.True(t, registered(t, dir, path))
	require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))
	err := cmd.Wait()
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 143, ee.ExitCode())
	assert.False(t, registered(t, dir, path))
	_, statErr := os.Stat(filepath.Dir(path))
	assert.True(t, os.IsNotExist(statErr))
}

func TestSnapshot_UnknownStartTimeKeepsLiveOwner(t *testing.T) {
	root := t.TempDir()
	own := func(content string) {
		require.NoError(t, os.WriteFile(filepath.Join(root, ownerFile), []byte(content), 0o644))
	}
	pid := itoa(os.Getpid())

	own(pid + "\n\n") // start time was unknown when written
	assert.True(t, ownerAlive(root), "empty recorded start, live pid: kept")

	own(pid + "\nMon Jan  1 00:00:00 2001\n")
	orig := processStart
	t.Cleanup(func() { processStart = orig })
	processStart = func(int) string { return "" } // ps fails now
	assert.True(t, ownerAlive(root), "ps error, live pid: kept")

	processStart = func(int) string { return "Tue Feb  2 00:00:00 2002" }
	assert.False(t, ownerAlive(root), "both known and differ: dead")

	own(itoa(deadPID(t)) + "\n\n")
	assert.False(t, ownerAlive(root), "pid gone: dead")
}
