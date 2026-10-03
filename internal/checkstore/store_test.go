package checkstore

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/checkcache"
)

func openTestStore(t *testing.T) *store {
	t.Helper()
	s := Open(checkcache.NewMemory(), false).(*store)
	t.Cleanup(func() { s.Close() })
	return s
}

// cached is the lookup of rule at hash h1, which every test run is recorded under.
func (s *store) cached(subject, kind, fingerprint string) (CachedCheck, bool, error) {
	return s.CachedCheck(rule, "h1", subject, kind, fingerprint)
}

func TestOpen_WritesTheFileAndItsDirectoryOnClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checks", "abc", "results.jsonl")
	s := Open(checkcache.OpenFile(path), false)
	_, err := s.RecordRun(run("h0"))
	require.NoError(t, err)
	require.NoError(t, s.Close())
	assert.FileExists(t, path)
}

func TestOpen_IsIdempotentAndKeepsWhatWasWritten(t *testing.T) {
	backend := checkcache.OpenFile(filepath.Join(t.TempDir(), "results.jsonl"))
	for range 3 {
		require.NoError(t, Open(backend, false).Close())
	}
	first := Open(backend, false)
	_, err := first.RecordRun(run("h0"))
	require.NoError(t, err)
	require.NoError(t, first.Close())

	second := Open(backend, false)
	defer second.Close()
	rows, err := second.Query(`select count(*) as n from check_runs`)
	require.NoError(t, err)
	assert.EqualValues(t, 1, rows[0]["n"])
}

// The tables are a10n's: a reader written for one reads the other. check_runs has one column more,
// agent_id: one cache serves the whole repository, and it says which agent ran a run.
func TestSchema_TablesAndColumnsAreA10nsNames(t *testing.T) {
	s := openTestStore(t)
	want := map[string][]string{
		"check_runs":  {"id", "run_batch_id", "run_at", "check_id", "repo_id", "branch", "session_id", "agent_id", "base_ref", "head_ref", "exit_code", "error", "metadata", "created_at"},
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

func TestOpenReadOnly_ReadsWhatTheWriterRecordedAndCannotWrite(t *testing.T) {
	backend := checkcache.NewMemory()
	w := Open(backend, false)
	id, err := w.RecordRun(run("h0"))
	require.NoError(t, err)
	_, err = w.RecordCheck(id, judge("fail", "fp"))
	require.NoError(t, err)
	require.NoError(t, w.Close()) // a store writes its runs when it closes

	r := Open(backend, true).(*store)
	defer r.Close()

	c, found, err := r.cached("changeset", "check[1]:judge:./rubric.md.j2", "fp")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, StatusFail, c.Status)

	_, err = r.RecordRun(run("h1"))
	assert.Error(t, err, "a read-only store cannot record")
	_, err = r.RecordCheck(id, judge("pass", "fp"))
	assert.Error(t, err, "a read-only store cannot record")
	assert.Error(t, r.FinishRun(id), "a read-only store cannot record")
	rows, err := r.Query(`select count(*) as n from check_runs`)
	require.NoError(t, err)
	assert.EqualValues(t, 1, rows[0]["n"])
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
