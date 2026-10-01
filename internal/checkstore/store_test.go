package checkstore

import (
	"database/sql"
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

// The tables are a10n's: a reader written for one reads the other. check_runs has one column more,
// agent_id: one database serves the whole session family, and it says which agent ran a run.
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

// A database an older engine created has no agent_id: opening it adds the column, keeps its rows.
func TestOpen_AddsTheAgentColumnToAnOlderDatabaseAndKeepsItsRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checks.db")
	old, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = old.Exec(`CREATE TABLE check_runs (id TEXT PRIMARY KEY, run_batch_id TEXT NOT NULL, run_at TEXT NOT NULL, check_id TEXT NOT NULL,
		repo_id TEXT NOT NULL, branch TEXT NOT NULL, session_id TEXT NOT NULL, base_ref TEXT NOT NULL DEFAULT '', head_ref TEXT NOT NULL DEFAULT '',
		exit_code INTEGER NOT NULL DEFAULT 0, error TEXT, metadata TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL DEFAULT (datetime('now')));
		INSERT INTO check_runs (id, run_batch_id, run_at, check_id, repo_id, branch, session_id, head_ref) VALUES ('run-old', 'b', 't', 'file-guard/x', 'r', 'main', 's', 'h0')`)
	require.NoError(t, err)
	require.NoError(t, old.Close())

	s, err := Open(path)
	require.NoError(t, err)
	defer s.Close()
	rows, err := s.Query(`select id, agent_id from check_runs`)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "run-old", rows[0]["id"])
	assert.Equal(t, "", rows[0]["agent_id"])
	id, err := s.RecordRun(CheckRun{RunIdentity: RunIdentity{RepoID: "r", Branch: "main", SessionID: "s", AgentID: "a1"}, BatchID: "b", CheckID: "file-guard/x", HeadRef: "h1"})
	require.NoError(t, err)
	rows, err = s.Query(`select agent_id from check_runs where id = '` + id + `'`)
	require.NoError(t, err)
	assert.Equal(t, "a1", rows[0]["agent_id"])
}

// A sub-agent's own database (an older engine's) is imported into the family's, tagged with
// the agent, once: importing again changes nothing, and the source is left alone.
func TestImport_BringsASubagentsRowsInOnceAndTagsThem(t *testing.T) {
	dir := t.TempDir()
	rootPath, subPath := filepath.Join(dir, "root", "checks.db"), filepath.Join(dir, "sub", "checks.db")
	root, err := Open(rootPath)
	require.NoError(t, err)
	defer root.Close()
	sub, err := Open(subPath)
	require.NoError(t, err)
	_, err = root.RecordRun(run("h-root"))
	require.NoError(t, err)
	subRun := run("h-sub")
	subRun.SessionID = "s-sub"
	subID, err := sub.RecordRun(subRun)
	require.NoError(t, err)
	_, err = sub.RecordCheck(subID, judge("fail", "fp-sub"))
	require.NoError(t, err)
	require.NoError(t, sub.FinishRun(subID))
	require.NoError(t, sub.Close())

	n, err := Import(root, subPath, "agent-7")
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	rows, err := root.Query(`select r.head_ref as head, r.agent_id as agent, c.status as status from check_runs r left join checks c on c.run_id = r.id order by r.head_ref`)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "h-sub", rows[1]["head"])
	assert.Equal(t, "agent-7", rows[1]["agent"])
	assert.Equal(t, "fail", rows[1]["status"], "the sub-agent's refusal is the family's too")
	assert.Equal(t, "", rows[0]["agent"], "the root's own run is not retagged")

	n, err = Import(root, subPath, "agent-7")
	require.NoError(t, err)
	assert.Equal(t, 0, n, "a second import changes nothing")
	rows, _ = root.Query(`select count(*) as n from check_runs`)
	assert.EqualValues(t, 2, rows[0]["n"])
}
