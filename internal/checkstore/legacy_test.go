package checkstore

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func legacyFile(t *testing.T, fn func(s Store)) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions", "ws", "s1", "checks.db")
	s, err := Open(path)
	require.NoError(t, err)
	fn(s)
	require.NoError(t, s.Close())
	return path
}

func TestFamilyStores_AReadersSQLCannotReachAnotherFamilyOrTheSharedTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checks.db")
	a, err := OpenFamily(path, "sess-a")
	require.NoError(t, err)
	defer a.Close()
	b, err := OpenFamily(path, "sess-b")
	require.NoError(t, err)
	defer b.Close()
	passRun(t, a, "headA", "fpA")
	for _, q := range []string{
		"select * from main.check_runs",
		"select * from main.legacy_imports",
		"select * from fam.check_runs",
		"select * from temp.check_runs",
		"select head_ref from main.check_runs",
	} {
		rows, err := b.Query(q)
		if err == nil {
			assert.Empty(t, rows, q)
		}
	}
}

func TestImportLegacy_ReplacesTheItemsOfACheckTheOldEngineRecordedAgain(t *testing.T) {
	var runID string
	path := legacyFile(t, func(s Store) {
		id, err := s.RecordRun(run("h1"))
		require.NoError(t, err)
		runID = id
		_, err = s.RecordCheck(id, CheckRecord{Subject: "changeset", Kind: "k", Status: StatusFail,
			Items: []CheckItem{{Key: "a.md", Passed: false}}})
		require.NoError(t, err)
		require.NoError(t, s.FinishRun(id))
	})
	dst, err := OpenFamily(filepath.Join(t.TempDir(), "r", "checks.db"), "s1")
	require.NoError(t, err)
	defer dst.Close()
	src := []Legacy{{Path: path, Family: "s1"}}
	require.NoError(t, ImportLegacy(dst, src))

	time.Sleep(5 * time.Millisecond)
	old, err := Open(path)
	require.NoError(t, err)
	_, err = old.RecordCheck(runID, CheckRecord{Subject: "changeset", Kind: "k", Status: StatusFail,
		Items: []CheckItem{{Key: "b.md", Passed: false}}})
	require.NoError(t, err)
	require.NoError(t, old.Close())
	require.NoError(t, ImportLegacy(dst, src))

	rows, err := dst.Query("select key from check_items order by key")
	require.NoError(t, err)
	require.Len(t, rows, 1, "the stale item is gone")
	assert.Equal(t, "b.md", rows[0]["key"])
}

func TestImportLegacy_AStopHoldingTheLockPostponesAndAKilledStopReleasesIt(t *testing.T) {
	path := legacyFile(t, func(s Store) { passRun(t, s, "h1", "fp") })
	dst, err := OpenFamily(filepath.Join(t.TempDir(), "r", "checks.db"), "s1")
	require.NoError(t, err)
	defer dst.Close()
	src := []Legacy{{Path: path, Family: "s1"}}

	// A Stop of another process holds the lock.
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperHoldStopLock")
	cmd.Env = append(os.Environ(), "CHECKSTORE_HOLD_LOCK="+lockPathFor(dst.Path(), path))
	out, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	buf := make([]byte, 5)
	_, err = out.Read(buf)
	require.NoError(t, err, "the helper took the lock")

	postponed, err := ImportLegacyReport(dst, src)
	require.NoError(t, err)
	assert.Equal(t, []string{path}, postponed)
	assert.True(t, LegacyBusy(path, dst.Path()))
	heads, err := dst.PassedHeads(rule)
	require.NoError(t, err)
	assert.Empty(t, heads, "nothing migrated while the Stop runs")

	require.NoError(t, cmd.Process.Kill())
	_ = cmd.Wait()
	postponed, err = ImportLegacyReport(dst, src)
	require.NoError(t, err)
	assert.Empty(t, postponed, "the kernel dropped the killed Stop's lock")
	heads, err = dst.PassedHeads(rule)
	require.NoError(t, err)
	assert.Equal(t, []string{"h1"}, heads)
}

func TestHelperHoldStopLock(t *testing.T) {
	path := os.Getenv("CHECKSTORE_HOLD_LOCK")
	if path == "" {
		t.Skip("helper process")
	}
	release := lockShared(path)
	defer release()
	os.Stdout.WriteString("held\n")
	time.Sleep(time.Minute)
}

func TestImportLegacy_AnOldBinarysRunningRowStillPostpones(t *testing.T) {
	path := legacyFile(t, func(s Store) {
		_, err := s.RecordRun(run("h-live")) // RUNNING, no lock: an engine older than the lock
		require.NoError(t, err)
	})
	dst, err := OpenFamily(filepath.Join(t.TempDir(), "r", "checks.db"), "s1")
	require.NoError(t, err)
	defer dst.Close()
	postponed, err := ImportLegacyReport(dst, []Legacy{{Path: path, Family: "s1"}})
	require.NoError(t, err)
	assert.Equal(t, []string{path}, postponed)
	assert.True(t, LegacyBusy(path, dst.Path()))
}

func TestOpenLegacy_HoldsTheLockUntilClosed(t *testing.T) {
	path := legacyFile(t, func(s Store) { passRun(t, s, "h1", "fp") })
	repoPath := filepath.Join(t.TempDir(), "r", "checks.db")
	st, err := OpenLegacy(path, repoPath, "s1")
	require.NoError(t, err)
	assert.True(t, LegacyBusy(path, repoPath))
	require.NoError(t, st.Close())
	assert.False(t, LegacyBusy(path, repoPath))
}
