package reap

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mk(t *testing.T, tmp, name string) string {
	t.Helper()
	d := filepath.Join(tmp, name)
	require.NoError(t, os.MkdirAll(filepath.Join(d, "x"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(d, "x", "f"), []byte("x"), 0o644))
	return d
}

func owner(t *testing.T, dir string, pid int) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, OwnerFile), []byte(strconv.Itoa(pid)+"\n"), 0o644))
}

func age(t *testing.T, dir string, by time.Duration) {
	t.Helper()
	old := time.Now().Add(-by)
	require.NoError(t, os.Chtimes(dir, old, old))
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func deadPid(t *testing.T) int {
	t.Helper()
	c := exec.Command("true")
	require.NoError(t, c.Run())
	return c.Process.Pid
}

func TestTempReapsOnlyWhatIsGarbage(t *testing.T) {
	tmp := t.TempDir()
	deadOwned := mk(t, tmp, "sr-test-dead")
	owner(t, deadOwned, deadPid(t))
	liveOwned := mk(t, tmp, "sr-test-live")
	owner(t, liveOwned, os.Getpid())
	oldLegacy := mk(t, tmp, "sr-agent-output111")
	age(t, oldLegacy, 48*time.Hour)
	freshLegacy := mk(t, tmp, "sr-agent-output222")
	oldTree := mk(t, tmp, "sr-tree-old")
	age(t, oldTree, 48*time.Hour)
	freshTree := mk(t, tmp, "sr-tree-fresh")
	unrelated := mk(t, tmp, "something-else")
	age(t, unrelated, 48*time.Hour)
	require.NoError(t, os.Chmod(filepath.Join(oldTree, "x"), 0o555)) // a read-only checkout

	assert.Equal(t, 3, Temp(tmp, ""))

	assert.False(t, exists(deadOwned), "a dead owner's directory is gone")
	assert.False(t, exists(oldLegacy), "an old ownerless directory is gone")
	assert.False(t, exists(oldTree), "an old ownerless snapshot directory is gone, read-only or not")
	assert.True(t, exists(liveOwned), "a live owner's directory stays")
	assert.True(t, exists(freshLegacy), "a young ownerless directory stays")
	assert.True(t, exists(freshTree), "a young ownerless snapshot directory stays")
	assert.True(t, exists(unrelated), "what is not ours stays")
}

func TestMarkNamesTheCurrentProcess(t *testing.T) {
	d := t.TempDir()
	Mark(d)
	gone, marked := ownerGone(d)
	assert.True(t, marked)
	assert.False(t, gone)
}
