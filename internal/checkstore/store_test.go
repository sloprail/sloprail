package checkstore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTestStore(t *testing.T) *store {
	t.Helper()
	s, err := open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpen_CreatesTheFileAndItsDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions", "abc", "checks.db")
	s, err := Open(path)
	require.NoError(t, err)
	defer s.Close()
	assert.FileExists(t, path)
}

func TestOpen_IsIdempotentAndKeepsWhatWasWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checks.db")
	for range 3 {
		s, err := Open(path)
		require.NoError(t, err)
		require.NoError(t, s.Close())
	}
	first, err := Open(path)
	require.NoError(t, err)
	_, err = first.RecordRun(run("h0"))
	require.NoError(t, err)
	require.NoError(t, first.Close())

	second, err := Open(path)
	require.NoError(t, err)
	defer second.Close()
	rows, err := second.Query(`select count(*) as n from check_runs`)
	require.NoError(t, err)
	assert.EqualValues(t, 1, rows[0]["n"])
}

// The tables are a10n's: a reader written for one reads the other.
func TestSchema_TablesAndColumnsAreA10nsNames(t *testing.T) {
	s := openTestStore(t)
	want := map[string][]string{
		"check_runs":  {"id", "run_batch_id", "run_at", "check_id", "repo_id", "branch", "session_id", "base_ref", "head_ref", "exit_code", "error", "metadata", "created_at"},
		"checks":      {"id", "run_id", "subject", "kind", "status", "fingerprint", "last_step", "output", "metadata", "checked_at"},
		"check_items": {"id", "check_id", "key", "passed", "metadata", "checked_at"},
	}
	for table, cols := range want {
		rows, err := s.Query(`select name from pragma_table_info('` + table + `') order by cid`)
		require.NoError(t, err, table)
		var got []string
		for _, r := range rows {
			got = append(got, r["name"].(string))
		}
		assert.Equal(t, cols, got, table)
	}
}

func TestOpenReadOnly_NoDatabaseIsErrNoStoreAndCreatesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checks.db")
	_, err := OpenReadOnly(path)
	assert.ErrorIs(t, err, ErrNoStore)
	assert.NoFileExists(t, path)
}

func TestOpenReadOnly_ReadsWhatTheWriterRecordedAndCannotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checks.db")
	w, err := Open(path)
	require.NoError(t, err)
	id, err := w.RecordRun(run("h0"))
	require.NoError(t, err)
	_, err = w.RecordCheck(id, judge("fail", "fp"))
	require.NoError(t, err)
	// The writer stays open: a reader must work beside it.

	r, err := OpenReadOnly(path)
	require.NoError(t, err)
	defer r.Close()

	c, found, err := r.CachedCheck("changeset", "check[1]:judge:./rubric.md.j2", "fp")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, StatusFail, c.Status)

	_, err = r.RecordRun(run("h1"))
	assert.Error(t, err, "a read-only store cannot record")
	rows, err := w.Query(`select count(*) as n from check_runs`)
	require.NoError(t, err)
	assert.EqualValues(t, 1, rows[0]["n"])
	require.NoError(t, w.Close())

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.False(t, info.IsDir())
}

func TestCheckStatus_FiltersByRule(t *testing.T) {
	s := openTestStore(t)
	record(t, s, run("h0"), script("pass"))
	other := run("h0")
	other.CheckID = "plug/file-guard/other"
	record(t, s, other, script("fail"))

	for _, name := range []string{"plug/file-guard/other", "other"} {
		rows, err := s.CheckStatus(false, name)
		require.NoError(t, err)
		require.Len(t, rows, 1, name)
		assert.Equal(t, "plug/file-guard/other", rows[0].Rule)
	}
	rows, err := s.CheckStatus(false, "nothing")
	require.NoError(t, err)
	assert.Empty(t, rows)
}
