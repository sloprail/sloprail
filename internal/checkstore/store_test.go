package checkstore

import (
	"database/sql"
	"os"
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

// oldDB creates a sqlite checks.db as an older engine kept it (schema.sql is its shape) and runs setup on it.
func oldDB(t *testing.T, path, ddl string, setup string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()
	if ddl == "" {
		ddl = schema
	}
	_, err = db.Exec(ddl)
	require.NoError(t, err)
	_, err = db.Exec(setup)
	require.NoError(t, err)
}

// A database an older engine created has no agent_id: importing it keeps its rows, tagged with
// the agent given.
func TestImport_AnOlderDatabaseWithoutTheAgentColumnKeepsItsRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checks.db")
	oldDB(t, path, `CREATE TABLE check_runs (id TEXT PRIMARY KEY, run_batch_id TEXT NOT NULL, run_at TEXT NOT NULL, check_id TEXT NOT NULL,
		repo_id TEXT NOT NULL, branch TEXT NOT NULL, session_id TEXT NOT NULL, base_ref TEXT NOT NULL DEFAULT '', head_ref TEXT NOT NULL DEFAULT '',
		exit_code INTEGER NOT NULL DEFAULT 0, error TEXT, metadata TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL DEFAULT (datetime('now')));
		CREATE TABLE checks (id TEXT PRIMARY KEY, run_id TEXT NOT NULL, subject TEXT NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL,
		fingerprint TEXT, last_step TEXT NOT NULL DEFAULT '', output TEXT NOT NULL DEFAULT '', metadata TEXT NOT NULL DEFAULT '{}', checked_at TEXT NOT NULL);
		CREATE TABLE check_items (id TEXT PRIMARY KEY, check_id TEXT NOT NULL, key TEXT, passed INTEGER NOT NULL DEFAULT 0, metadata TEXT NOT NULL DEFAULT '{}', checked_at TEXT NOT NULL)`,
		`INSERT INTO check_runs (id, run_batch_id, run_at, check_id, repo_id, branch, session_id, head_ref) VALUES ('run-old', 'b', 't', 'file-guard/x', 'r', 'main', 's', 'h0')`)

	s := openTestStore(t)
	n, err := Import(s, path, "a1")
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	rows, err := s.Query(`select id, agent_id from check_runs`)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "run-old", rows[0]["id"])
	assert.Equal(t, "a1", rows[0]["agent_id"])
}

// A sub-agent's own database (an older engine's) is imported into the family's, tagged with
// the agent, once: importing again changes nothing, and the source is left alone.
func TestImport_BringsASubagentsRowsInOnceAndTagsThem(t *testing.T) {
	dir := t.TempDir()
	subPath := filepath.Join(dir, "sub", "checks.db")
	root := openTestStore(t)
	_, err := root.RecordRun(run("h-root"))
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(subPath), 0o755))
	oldDB(t, subPath, "", `
		INSERT INTO check_runs (id, run_batch_id, run_at, check_id, repo_id, branch, session_id, agent_id, base_ref, head_ref, metadata)
			VALUES ('run_sub', 'batch-1', '2026-01-01T00:00:00.000000000Z', 'file-guard/size', 'root0', 'main', 's-sub', '', 'base0', 'h-sub', '{"ruleHash":"h1","state":"complete"}');
		INSERT INTO checks (id, run_id, subject, kind, status, fingerprint, metadata, checked_at)
			VALUES ('chk_sub', 'run_sub', 'changeset', 'check[1]:judge:./rubric.md.j2', 'fail', 'fp-sub', '{"reasoning":"because fail"}', '2026-01-01T00:00:00.000000000Z');
		INSERT INTO check_items (id, check_id, key, passed, metadata, checked_at) VALUES ('itm_sub', 'chk_sub', 'a.go', 0, '{}', 't')`)

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
	items, err := root.Query(`select key from check_items`)
	require.NoError(t, err)
	require.Len(t, items, 1)

	n, err = Import(root, subPath, "agent-7")
	require.NoError(t, err)
	assert.Equal(t, 0, n, "a second import changes nothing")
	rows, _ = root.Query(`select count(*) as n from check_runs`)
	assert.EqualValues(t, 2, rows[0]["n"])
}

// Import is the migration path: an old sqlite store's results land in the cache and are found there.
func TestImport_AnOldStoresResultsReachTheCacheAndAreFoundThere(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checks.db")
	oldDB(t, path, "", `
		INSERT INTO check_runs (id, run_batch_id, run_at, check_id, repo_id, branch, session_id, base_ref, head_ref, metadata)
			VALUES ('run_old', 'b', '2026-01-01T00:00:00.000000000Z', 'file-guard/size', 'root0', 'main', 's', 'base0', 'h0', '{"ruleHash":"h1","state":"complete"}');
		INSERT INTO checks (id, run_id, subject, kind, status, fingerprint, metadata, checked_at)
			VALUES ('chk_old', 'run_old', 'changeset', 'check[1]:judge:./rubric.md.j2', 'pass', 'fp-old', '{"reasoning":"fine"}', '2026-01-01T00:00:00.000000000Z')`)
	backend := checkcache.NewMemory()
	s := Open(backend, false)
	_, err := Import(s, path, "")
	require.NoError(t, err)
	require.NoError(t, s.Close())

	got := Open(backend, true)
	defer got.Close()
	c, found, err := got.CachedCheck(rule, "h1", "changeset", "check[1]:judge:./rubric.md.j2", "fp-old")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "fine", c.Metadata["reasoning"])
	heads, err := got.PassedHeads(rule)
	require.NoError(t, err)
	assert.Equal(t, []string{"h0"}, heads)
}
