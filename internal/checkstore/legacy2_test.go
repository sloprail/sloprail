package checkstore

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rawExec(t *testing.T, path, q string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(q, args...)
	require.NoError(t, err)
}

func failedRun(t *testing.T, s Store, head string) {
	t.Helper()
	id, err := s.RecordRun(run(head))
	require.NoError(t, err)
	_, err = s.RecordCheck(id, CheckRecord{Subject: "changeset", Kind: "k", Status: StatusFail})
	require.NoError(t, err)
	require.NoError(t, s.FinishRun(id))
}

func newRepoStore(t *testing.T) Store {
	t.Helper()
	dst, err := OpenFamily(filepath.Join(t.TempDir(), "r", "checks.db"), "s1")
	require.NoError(t, err)
	t.Cleanup(func() { dst.Close() })
	return dst
}

func TestUnionReadOnly_SeesTheRepositoryAndTheOldFilesNewerRowsAtOnce(t *testing.T) {
	path := legacyFile(t, func(s Store) { failedRun(t, s, "hOld") })
	dst := newRepoStore(t)
	require.NoError(t, ImportLegacy(dst, []Legacy{{Path: path, Family: "s1", Folder: "/wt"}}))
	_, err := dst.RecordRun(run("hNew")) // the new engine's own, newer row in the repository
	require.NoError(t, err)

	// An older engine writes after the import.
	old, err := Open(path)
	require.NoError(t, err)
	failedRun(t, old, "hLate")
	require.NoError(t, old.Close())
	assert.False(t, Imported(dst.Path(), path), "the old file changed since the import")

	u, err := OpenUnionReadOnly(dst.Path(), "s1", []Legacy{{Path: path, Family: "s1"}})
	require.NoError(t, err)
	defer u.Close()
	refs, err := u.RunRefs(rule)
	require.NoError(t, err)
	var heads []string
	for _, f := range refs.Failed {
		heads = append(heads, f.Head)
	}
	assert.ElementsMatch(t, []string{"hOld", "hLate", "hNew"}, append(heads, "hNew"), "old, late and repository rows, once each")
	rows, err := u.Query("select count(*) as n from check_runs")
	require.NoError(t, err)
	assert.EqualValues(t, 3, rows[0]["n"])
}

func TestUnionReadOnly_ARefusalResolvedInTheRepositoryDoesNotComeBackFromTheOldFile(t *testing.T) {
	path := legacyFile(t, func(s Store) { failedRun(t, s, "hBad") })
	dst := newRepoStore(t)
	require.NoError(t, ImportLegacy(dst, []Legacy{{Path: path, Family: "s1"}}))
	live, err := dst.RecordRun(run("hLive"))
	require.NoError(t, err)
	_, err = dst.RecordCheck(live, CheckRecord{Subject: "changeset", Kind: "k", Status: StatusPass, Fingerprint: "other"})
	require.NoError(t, err)
	n, err := dst.ResolveStale(rule, "h1", live)
	require.NoError(t, err)
	require.Equal(t, 1, n, "the imported refusal was resolved in the repository")

	u, err := OpenUnionReadOnly(dst.Path(), "s1", []Legacy{{Path: path, Family: "s1"}})
	require.NoError(t, err)
	defer u.Close()
	refs, err := u.RunRefs(rule)
	require.NoError(t, err)
	assert.Empty(t, refs.Failed)
}

func TestImportLegacy_ALockThatCannotBeMadeIsNotAStopRunning(t *testing.T) {
	path := legacyFile(t, func(s Store) { passRun(t, s, "h1", "fp") })
	dst := newRepoStore(t)
	// Nothing can be created where the lock files go.
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(dst.Path()), "locks"), []byte("x"), 0o644))
	postponed, err := ImportLegacyReport(dst, []Legacy{{Path: path, Family: "s1"}})
	require.NoError(t, err)
	assert.Empty(t, postponed, "only a HELD lock postpones; the RUNNING-row check found nothing")
	heads, err := dst.PassedHeads(rule)
	require.NoError(t, err)
	assert.Equal(t, []string{"h1"}, heads)
}

func TestImportLegacy_TheLockLivesBesideTheRepositoryDatabaseNotTheOldFile(t *testing.T) {
	path := legacyFile(t, func(s Store) { passRun(t, s, "h1", "fp") })
	dst := newRepoStore(t)
	require.NoError(t, ImportLegacy(dst, []Legacy{{Path: path, Family: "s1"}}))
	assert.NoFileExists(t, path+".stop-lock")
	assert.FileExists(t, lockPathFor(dst.Path(), path))
}

// runningOld is an old-layout file with one run recorded RUNNING, and that run's id.
func runningOld(t *testing.T) (string, string) {
	t.Helper()
	var id string
	path := legacyFile(t, func(s Store) {
		var err error
		id, err = s.RecordRun(run("hRunning"))
		require.NoError(t, err)
	})
	return path, id
}

func deadPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	require.NoError(t, cmd.Run())
	return cmd.Process.Pid
}

func TestImportLegacy_ARunningRunWhoseRecordedOwnerIsGoneDoesNotPostpone(t *testing.T) {
	path, id := runningOld(t)
	dst := newRepoStore(t)
	host, _ := os.Hostname()
	// Owners are recorded in the repository database (by a new engine's Stop on the old file).
	rawExec(t, dst.Path(), `INSERT INTO run_owners (run_id, pid, host) VALUES (?, ?, ?)`, id, deadPid(t), host)
	postponed, err := ImportLegacyReport(dst, []Legacy{{Path: path, Family: "s1"}})
	require.NoError(t, err)
	assert.Empty(t, postponed, "its process is provably gone")
}

func TestLockedStore_RecordsItsRunsOwnerInTheRepositoryAndForgetsItAtFinish(t *testing.T) {
	dst := newRepoStore(t)
	path := legacyFile(t, func(s Store) { passRun(t, s, "h0", "fp") })
	l, err := OpenLegacy(path, dst.Path(), "s1")
	require.NoError(t, err)
	defer l.Close()
	id, err := l.RecordRun(run("h1"))
	require.NoError(t, err)
	var n int
	db, err := sql.Open("sqlite", dst.Path())
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM run_owners WHERE run_id = ?`, id).Scan(&n))
	assert.Equal(t, 1, n)
	require.NoError(t, l.FinishRun(id))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM run_owners WHERE run_id = ?`, id).Scan(&n))
	assert.Equal(t, 0, n)
}

func TestImportLegacy_ARunningRunWithALiveOwnerPostponesAndAnOldOneWithoutOwnerUsesTheStopTimeout(t *testing.T) {
	p, id := runningOld(t)
	dst := newRepoStore(t)
	host, _ := os.Hostname()
	rawExec(t, dst.Path(), `INSERT INTO run_owners (run_id, pid, host) VALUES (?, ?, ?)`, id, os.Getpid(), host)
	postponed, err := ImportLegacyReport(dst, []Legacy{{Path: p, Family: "s1"}})
	require.NoError(t, err)
	assert.Equal(t, []string{p}, postponed, "this very test process owns it, and it is alive")

	// An older engine's run: no owner record. 20 minutes is inside the Stop timeout, 2 hours is not.
	old := legacyFile(t, func(s Store) {
		_, err := s.RecordRun(run("hOld"))
		require.NoError(t, err)
	})
	at := func(d time.Duration) string { return time.Now().Add(-d).UTC().Format("2006-01-02T15:04:05.000000000Z") }
	rawExec(t, old, `UPDATE check_runs SET run_at = ?`, at(20*time.Minute))
	postponed, err = ImportLegacyReport(dst, []Legacy{{Path: old, Family: "s1"}})
	require.NoError(t, err)
	assert.Equal(t, []string{old}, postponed, "a 20-minute-old RUNNING row may still be a Stop (timeout is an hour)")
	rawExec(t, old, `UPDATE check_runs SET run_at = ?`, at(2*time.Hour))
	postponed, err = ImportLegacyReport(dst, []Legacy{{Path: old, Family: "s1"}})
	require.NoError(t, err)
	assert.Empty(t, postponed, "past the Stop timeout it is a crashed one")
}

func TestLockedStore_ReadsTheRepositorySiblingsBesideTheOldFile(t *testing.T) {
	dst := newRepoStore(t)
	other, err := OpenFamily(dst.Path(), "other")
	require.NoError(t, err)
	defer other.Close()
	r := run("hOther")
	r.Folder = "/wt"
	id, err := other.RecordRun(r)
	require.NoError(t, err)
	_, err = other.RecordCheck(id, CheckRecord{Subject: "changeset", Kind: "k", Status: StatusFail})
	require.NoError(t, err)
	require.NoError(t, other.FinishRun(id))

	path := legacyFile(t, func(s Store) { passRun(t, s, "h1", "fp") })
	l, err := OpenLegacy(path, dst.Path(), "s1")
	require.NoError(t, err)
	defer l.Close()
	refs, err := l.(SiblingRefs).SiblingRunRefs(rule, "/wt")
	require.NoError(t, err)
	require.Len(t, refs.Failed, 1)
	assert.Equal(t, "hOther", refs.Failed[0].Head)
}
