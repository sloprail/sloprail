package repochecks

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/sessionpath"
)

func sh(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func repo(t *testing.T) string {
	t.Helper()
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	sh(t, dir, "init", "-q", "-b", "main")
	sh(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "root")
	return dir
}

func TestRepoChecksDB_IsTheSameFromEveryTreeOfTheRepository(t *testing.T) {
	dir := repo(t)
	wt := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-wt")
	sh(t, dir, "worktree", "add", "-q", "-b", "other", wt)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))

	a, err := sessionpath.RepoChecksDB(dir)
	require.NoError(t, err)
	b, err := sessionpath.RepoChecksDB(wt)
	require.NoError(t, err)
	c, err := sessionpath.RepoChecksDB(filepath.Join(dir, "sub"))
	require.NoError(t, err)
	assert.Equal(t, a, b, "a linked worktree is the same repository")
	assert.Equal(t, a, c, "so is a subdirectory")
	assert.Contains(t, a, filepath.Join("sloprail", "repos"))

	other := repo(t)
	d, err := sessionpath.RepoChecksDB(other)
	require.NoError(t, err)
	assert.NotEqual(t, a, d, "another repository has its own database")
}

func TestOpen_ImportsALiveSessionsOldDatabaseAndKeepsItWorking(t *testing.T) {
	dir := repo(t)
	old, err := sessionpath.ChecksDB(dir, "live-session")
	require.NoError(t, err)
	s, err := checkstore.Open(old)
	require.NoError(t, err)
	id, err := s.RecordRun(checkstore.CheckRun{BatchID: "b", CheckID: "p/file-guard/x", BaseRef: "b0", HeadRef: "h1",
		Metadata: map[string]any{"ruleHash": "r"}, Complete: true})
	require.NoError(t, err)
	require.NotEmpty(t, id)
	require.NoError(t, s.Close())

	st, err := Open(dir, "live-session", os.Stderr)
	require.NoError(t, err)
	heads, err := st.PassedHeads("p/file-guard/x")
	require.NoError(t, err)
	assert.Equal(t, []string{"h1"}, heads, "what the session had under the old layout is still its own")
	require.NoError(t, st.Close())
	assert.FileExists(t, old, "the old file is left as it was")

	// A reader sees the same, and a second open does not duplicate it.
	st, err = Open(dir, "live-session", os.Stderr)
	require.NoError(t, err)
	rows, err := st.Query("select count(*) as n from check_runs")
	require.NoError(t, err)
	assert.EqualValues(t, 1, rows[0]["n"])
	st.Close()
	ro, err := OpenReadOnly(dir, "live-session")
	require.NoError(t, err)
	defer ro.Close()
	heads, err = ro.PassedHeads("p/file-guard/x")
	require.NoError(t, err)
	assert.Equal(t, []string{"h1"}, heads)
}

func TestOpenReadOnly_AFamilyThatRecordedNothingHasNoStoreEvenWhenTheRepositoryDoes(t *testing.T) {
	dir := repo(t)
	w, err := Open(dir, "busy", nil)
	require.NoError(t, err)
	_, err = w.RecordRun(checkstore.CheckRun{BatchID: "b", CheckID: "p/file-guard/x", HeadRef: "h", Complete: true})
	require.NoError(t, err)
	w.Close()

	_, err = OpenReadOnly(dir, "quiet")
	assert.ErrorIs(t, err, checkstore.ErrNoStore)
}

func TestOpen_SeveralSessionsWriteTheOneDatabaseAtOnce(t *testing.T) {
	dir := repo(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8*10)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st, err := Open(dir, fmt.Sprintf("session-%d", i), nil)
			if err != nil {
				errs <- err
				return
			}
			defer st.Close()
			for j := 0; j < 10; j++ {
				if _, err := st.RecordRun(checkstore.CheckRun{BatchID: "b", CheckID: "p/file-guard/x", HeadRef: fmt.Sprintf("h%d", j), Complete: true}); err != nil {
					errs <- err
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	for i := 0; i < 8; i++ {
		ro, err := OpenReadOnly(dir, fmt.Sprintf("session-%d", i))
		require.NoError(t, err)
		heads, err := ro.PassedHeads("p/file-guard/x")
		require.NoError(t, err)
		assert.Len(t, heads, 10, "each session sees exactly its own runs")
		ro.Close()
	}
}
