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

// oldLayout writes a session's check results the way main wrote them before the repository
// database: one file per session beside its state.db.
func oldLayout(t *testing.T, dir, session string, fn func(s checkstore.Store)) string {
	t.Helper()
	path, err := sessionpath.ChecksDB(dir, session)
	require.NoError(t, err)
	s, err := checkstore.Open(path)
	require.NoError(t, err)
	fn(s)
	require.NoError(t, s.Close())
	return path
}

func failedRun(t *testing.T, s checkstore.Store, head string) string {
	t.Helper()
	id, err := s.RecordRun(checkstore.CheckRun{BatchID: "b", CheckID: "p/file-guard/x", BaseRef: "b0", HeadRef: head,
		Metadata: map[string]any{"ruleHash": "r"}})
	require.NoError(t, err)
	_, err = s.RecordCheck(id, checkstore.CheckRecord{Subject: "changeset", Kind: "k", Status: checkstore.StatusFail})
	require.NoError(t, err)
	require.NoError(t, s.FinishRun(id))
	return id
}

func TestMigration_AnOldSessionKeepsItsRefusalsAndPasses(t *testing.T) {
	dir := repo(t)
	oldLayout(t, dir, "s1", func(s checkstore.Store) {
		failedRun(t, s, "hBad")
		_, err := s.RecordRun(checkstore.CheckRun{BatchID: "b", CheckID: "p/file-guard/x", BaseRef: "b0", HeadRef: "hGood", Complete: true})
		require.NoError(t, err)
	})
	st, err := Open(dir, "s1", os.Stderr)
	require.NoError(t, err)
	defer st.Close()
	refs, err := st.RunRefs("p/file-guard/x")
	require.NoError(t, err)
	require.Len(t, refs.Failed, 1, "the refusal is still owed")
	assert.Equal(t, "hBad", refs.Failed[0].Head)
	require.Len(t, refs.Passed, 1)
	status, err := st.CheckStatus(false, "")
	require.NoError(t, err)
	assert.NotEmpty(t, status)
}

func TestMigration_ARunningRunPostponesItAndTheCycleUsesTheOldFile(t *testing.T) {
	dir := repo(t)
	var runID string
	path := oldLayout(t, dir, "s1", func(s checkstore.Store) {
		failedRun(t, s, "hBad")
		id, err := s.RecordRun(checkstore.CheckRun{BatchID: "b", CheckID: "p/file-guard/x", BaseRef: "b0", HeadRef: "hLive"})
		require.NoError(t, err)
		runID = id // recorded RUNNING, never finished: a Stop is evaluating
	})
	st, err := Open(dir, "s1", os.Stderr)
	require.NoError(t, err)
	assert.Equal(t, path, st.Path(), "this cycle keeps the old layout, nothing it recorded is hidden")
	refs, err := st.RunRefs("p/file-guard/x")
	require.NoError(t, err)
	assert.Len(t, refs.Failed, 1)
	st.Close()
	repoPath, err := sessionpath.RepoChecksDB(dir)
	require.NoError(t, err)
	if ro, err := checkstore.OpenFamilyReadOnly(repoPath, "s1"); err == nil {
		rows, _ := ro.Query("select count(*) as n from check_runs")
		assert.EqualValues(t, 0, rows[0]["n"], "nothing was migrated while the Stop ran")
		ro.Close()
	}

	// The old Stop finishes (a late write by the older binary): the next hook migrates.
	old, err := checkstore.Open(path)
	require.NoError(t, err)
	require.NoError(t, old.FinishRun(runID))
	require.NoError(t, old.Close())
	st, err = Open(dir, "s1", os.Stderr)
	require.NoError(t, err)
	defer st.Close()
	assert.Equal(t, repoPath, st.Path())
	rows, err := st.Query("select count(*) as n from check_runs")
	require.NoError(t, err)
	assert.EqualValues(t, 2, rows[0]["n"])
}

func TestMigration_AnOldLayoutWriteAfterTheImportIsPickedUpNextHook(t *testing.T) {
	dir := repo(t)
	var runID string
	path := oldLayout(t, dir, "s1", func(s checkstore.Store) { failedRun(t, s, "h1") })
	st, err := Open(dir, "s1", nil)
	require.NoError(t, err)
	st.Close()

	// An older binary, still running when the new one was installed, writes after the import:
	// a new run, and a run finishing that was already running at import time is not possible
	// here (running runs postpone), so a new refusal is the late write.
	old, err := checkstore.Open(path)
	require.NoError(t, err)
	runID = failedRun(t, old, "h2")
	require.NoError(t, old.Close())
	require.NotEmpty(t, runID)

	st, err = Open(dir, "s1", nil)
	require.NoError(t, err)
	defer st.Close()
	refs, err := st.RunRefs("p/file-guard/x")
	require.NoError(t, err)
	assert.Len(t, refs.Failed, 2, "the late write was not lost")
	rows, err := st.Query("select count(*) as n from check_runs")
	require.NoError(t, err)
	assert.EqualValues(t, 2, rows[0]["n"], "and nothing was duplicated")
}

func TestOpenReadOnly_WritesNothingAndReadsTheOldFileWhereItIsNotMigrated(t *testing.T) {
	dir := repo(t)
	oldLayout(t, dir, "s1", func(s checkstore.Store) { failedRun(t, s, "hBad") })
	repoPath, err := sessionpath.RepoChecksDB(dir)
	require.NoError(t, err)

	ro, err := OpenReadOnly(dir, "s1")
	require.NoError(t, err)
	defer ro.Close()
	refs, err := ro.RunRefs("p/file-guard/x")
	require.NoError(t, err)
	assert.Len(t, refs.Failed, 1, "the refusal in the old file is visible to a reader")
	assert.NoFileExists(t, repoPath, "a reader creates and migrates nothing")
}

func TestOpenReadOnly_AnOldEnginesRunningStopKeepsTheReaderOnTheOldFile(t *testing.T) {
	dir := repo(t)
	_ = oldLayout(t, dir, "s1", func(s checkstore.Store) {
		failedRun(t, s, "hBad")
		_, err := s.RecordRun(checkstore.CheckRun{BatchID: "b", CheckID: "p/file-guard/x", HeadRef: "hLive"})
		require.NoError(t, err) // RUNNING: a Stop of the older binary is evaluating
	})
	st, err := Open(dir, "s1", nil)
	require.NoError(t, err)
	st.Close()

	ro, err := OpenReadOnly(dir, "s1")
	require.NoError(t, err)
	defer ro.Close()
	refs, err := ro.RunRefs("p/file-guard/x")
	require.NoError(t, err)
	assert.Len(t, refs.Failed, 1)
}

func TestOpen_AReaderFirstNeverMisfilesASubagentsDatabase(t *testing.T) {
	dir := repo(t)
	oldLayout(t, dir, "root1", func(s checkstore.Store) { failedRun(t, s, "hRoot") })
	sub := oldLayout(t, dir, "sub1", func(s checkstore.Store) { failedRun(t, s, "hSub") })

	// A reader of the root family goes first, then the writer, who knows the sub-agent's file.
	if ro, err := OpenReadOnly(dir, "root1"); err == nil {
		ro.Close()
	}
	st, err := Open(dir, "root1", nil, checkstore.Legacy{Path: sub, Family: "root1", Agent: "agent-x", Folder: dir})
	require.NoError(t, err)
	defer st.Close()
	refs, err := st.RunRefs("p/file-guard/x")
	require.NoError(t, err)
	assert.Len(t, refs.Failed, 2, "the sub-agent's refusal is the root family's")
}

func TestOpenReadOnly_SeesTheRepositoryAndAnOldWriteMadeAfterTheImport(t *testing.T) {
	dir := repo(t)
	path := oldLayout(t, dir, "s1", func(s checkstore.Store) { failedRun(t, s, "h1") })
	st, err := Open(dir, "s1", nil)
	require.NoError(t, err)
	_, err = st.RecordRun(checkstore.CheckRun{BatchID: "b", CheckID: "p/file-guard/x", BaseRef: "b0", HeadRef: "hNew", Complete: true})
	require.NoError(t, err)
	st.Close()
	old, err := checkstore.Open(path)
	require.NoError(t, err)
	failedRun(t, old, "hLate")
	require.NoError(t, old.Close())

	ro, err := OpenReadOnly(dir, "s1")
	require.NoError(t, err)
	defer ro.Close()
	refs, err := ro.RunRefs("p/file-guard/x")
	require.NoError(t, err)
	assert.Len(t, refs.Failed, 2, "the imported refusal and the late one")
	assert.Len(t, refs.Passed, 1, "and the repository's own newer run")
}

func TestOpenReadOnly_AlsoSeesASubagentsOldFileNotYetImported(t *testing.T) {
	dir := repo(t)
	oldLayout(t, dir, "root1", func(s checkstore.Store) { failedRun(t, s, "hRoot") })
	sub := oldLayout(t, dir, "sub1", func(s checkstore.Store) { failedRun(t, s, "hSub") })
	src := checkstore.Legacy{Path: sub, Family: "root1", Agent: "agent-x", Folder: dir}

	ro, err := OpenReadOnly(dir, "root1", src)
	require.NoError(t, err)
	defer ro.Close()
	refs, err := ro.RunRefs("p/file-guard/x")
	require.NoError(t, err)
	assert.Len(t, refs.Failed, 2, "the sub-agent's refusal counts before it is migrated")
}

func TestMigration_ConcurrentHooksMigrateOnceWithoutDuplicates(t *testing.T) {
	dir := repo(t)
	oldLayout(t, dir, "s1", func(s checkstore.Store) {
		for i := 0; i < 20; i++ {
			failedRun(t, s, fmt.Sprintf("h%d", i))
		}
	})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, err := Open(dir, "s1", nil)
			if err != nil {
				errs <- err
				return
			}
			st.Close()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	st, err := Open(dir, "s1", nil)
	require.NoError(t, err)
	defer st.Close()
	rows, err := st.Query("select count(*) as n from check_runs")
	require.NoError(t, err)
	assert.EqualValues(t, 20, rows[0]["n"])
}
