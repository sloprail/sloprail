// Package checkstore holds what file-guards concluded about commits.
//
// The tables — check_runs, checks, check_items — and their columns are a10n's
// check-results store, so anything that reads one reads the other; only the
// data inside is sloprail's. See schema.sql for what each row means here.
//
// A file-guard is commit-based, so only file-guards write here. Gates and
// contexts are about events and leave no rows; the session's own memory
// (baseline, guardrail state) stays in sessionstate.
//
// One database per session FAMILY, the root session's checks.db beside its state.db, written
// by the root and by every sub-agent it dispatches (each run carries the agent_id that ran it).
// A check result is keyed by the rule's version and a commit range, a statement about the
// bytes and not about any one agent's working tree, so it is shared across the family; the
// per-agent state.db is not (sessionpath.StateDB says why). The identity columns on every run
// (repo, branch, session, agent) are what let the same rows move to one machine-wide database
// later without a change of shape. Import brings in the rows an older engine kept in a
// sub-agent's own database.
//
// This is the only package that opens the file. The writer (the Stop
// evaluation) opens it read-write; a reader (`sr-checks`, `sr-session
// changeset`) opens it with OpenReadOnly, which cannot create it, migrate it or
// write to it — reading what a session concluded must never be able to change it.
package checkstore

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	// The database is this package's resource, so the driver is its import.
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

// ErrClosed is returned by every method once the store has been closed.
var ErrClosed = errors.New("checkstore: store is closed")

// ErrNoStore reports that there is no check-results database to read: nothing
// has been checked in this session yet. A sentinel because the two readers
// treat it differently — a status listing shows nothing, a SQL query cannot run.
var ErrNoStore = errors.New("checkstore: no check results recorded for this session")

// Store is one session's check results.
type Store interface {
	// RecordRun stores one rule's evaluation of one commit range and returns the
	// run's id. See CheckRun.
	RecordRun(r CheckRun) (string, error)
	// FinishRun marks a run recorded RUNNING (CheckRun.Complete false) complete.
	// Only a complete run can be a watermark.
	FinishRun(runID string) error
	// RecordCheck stores one check of a run — replacing the same (subject, kind)
	// of that run — with its findings, and returns the check's id.
	RecordCheck(runID string, c CheckRecord) (string, error)
	// CachedCheck finds a stored pass or fail for (subject, kind, fingerprint):
	// the most recent one. A fail is returned like a pass — it is terminal and is
	// replayed, never re-judged until the input changes. An empty fingerprint
	// (a script) never hits.
	CachedCheck(subject, kind, fingerprint string) (CachedCheck, bool, error)
	// ResolveStale marks as skip every failing check of rule (at this rule hash, in COMPLETE runs)
	// outside run liveRunID whose (subject, kind, fingerprint) is not one liveRunID
	// holds: a failure whose input has left the range. Returns how many.
	ResolveStale(rule, ruleHash, liveRunID string) (int, error)
	// PassedHeads lists, newest first, the head_ref of each of the rule's runs, at any
	// rule hash, that passed: no engine error and no failing check. The
	// caller picks the first still reachable — that is the rule's watermark.
	PassedHeads(rule string) ([]string, error)
	// RunRefs lists, for one rule, the runs that were refused and the runs that
	// passed, each with the commit range it judged and when it ran. See RunRefs.
	RunRefs(rule string) (RunRefs, error)
	// Path is the database file this store was opened on.
	Path() string
	// CheckStatus lists each rule's latest run and its checks. failingOnly keeps
	// only what is failing: a failed engine run, or a fail/error/interrupted check.
	// A non-empty rule keeps only that rule.
	CheckStatus(failingOnly bool, rule string) ([]CheckStatusRow, error)
	// Query runs a read-only SELECT over the check tables and returns its rows.
	Query(sql string) ([]map[string]any, error)
	// Close releases the database.
	Close() error
}

type store struct {
	db   *sql.DB
	path string
	// family, when set, scopes every read to the runs of one session family (temp views named
	// like the tables, see scopeToFamily) and stamps every run written; "" is an unscoped store.
	family string
}

// Open opens the check-results database at path read-write, creating it, its
// directory and its tables when they are not there yet.
func Open(path string) (Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("checkstore: mkdir %s: %w", filepath.Dir(path), err)
	}
	return open(path)
}

// OpenReadOnly opens an EXISTING database for reading. It never creates the file
// and never writes: the connection is read-only at the driver, so even a bug
// here could not alter what a session concluded. ErrNoStore when there is no
// database.
func OpenReadOnly(path string) (Store, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoStore
		}
		return nil, fmt.Errorf("checkstore: %w", err)
	}
	dsn := "file:" + url.PathEscape(path) + "?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("checkstore: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("checkstore: open %s: %w", path, err)
	}
	return &store{db: db, path: path}, nil
}

// open is Open without the directory, which is also what the tests use against
// an in-memory database.
func open(path string) (*store, error) {
	dsn := path + "?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("checkstore: open %s: %w", path, err)
	}
	// Serialises this process's own writers; the cross-process half is busy_timeout's.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("checkstore: apply schema: %w", err)
	}
	if err := ensureAgentColumn(db); err != nil {
		db.Close()
		return nil, err
	}

	return &store{db: db, path: path}, nil
}

func (s *store) Path() string { return s.path }

// OpenFamily opens the repository's check-results database read-write, scoped to one session
// family: every read sees only that family's runs and every run written carries it. The
// database is shared by every session of the repository, so this scope is what keeps one
// session's open refusals and owed work its own (the one thing that is not shared is a pass on
// exactly the same input, see CachedCheck).
func OpenFamily(path, family string) (Store, error) {
	if family == "" {
		return nil, fmt.Errorf("checkstore: a family store needs a family")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("checkstore: mkdir %s: %w", filepath.Dir(path), err)
	}
	st, err := open(path)
	if err != nil {
		return nil, err
	}
	if err := ensureSharedColumns(st.db); err != nil {
		st.db.Close()
		return nil, err
	}
	if err := scopeToFamily(st.db, family); err != nil {
		st.db.Close()
		return nil, err
	}
	st.family = family
	return st, nil
}

// OpenFamilyReadOnly is OpenFamily for a reader: it never creates the file and cannot write.
// ErrNoStore when there is no database.
func OpenFamilyReadOnly(path, family string) (Store, error) {
	if family == "" {
		return nil, fmt.Errorf("checkstore: a family store needs a family")
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoStore
		}
		return nil, fmt.Errorf("checkstore: %w", err)
	}
	dsn := "file:" + url.PathEscape(path) + "?mode=ro&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("checkstore: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("checkstore: open %s: %w", path, err)
	}
	if err := scopeToFamily(db, family); err != nil {
		db.Close()
		return nil, err
	}
	// Views are made while the connection can still write its temp schema; from here it cannot.
	if _, err := db.Exec(`PRAGMA query_only = ON`); err != nil {
		db.Close()
		return nil, err
	}
	return &store{db: db, path: path, family: family}, nil
}

// scopeToFamily makes the three tables read as one family's: temp views of the same names
// shadow the main tables on the store's single connection, so every query written against
// check_runs / checks / check_items (the package's own, and a reader's `sr-checks sql`)
// sees exactly what a database of its own would hold. Writers name main.<table>.
// The columns each view exposes: exactly the unshared database's, so what a reader sees (and
// `select *` returns) does not change with where the rows are kept.
const (
	runCols   = "id, run_batch_id, run_at, check_id, repo_id, branch, session_id, agent_id, base_ref, head_ref, exit_code, error, metadata, created_at, rowid AS rowid"
	checkCols = "id, run_id, subject, kind, status, fingerprint, last_step, output, metadata, checked_at, rowid AS rowid"
	itemCols  = "id, check_id, key, passed, metadata, checked_at, rowid AS rowid"
)

func scopeToFamily(db *sql.DB, family string) error {
	lit := "'" + strings.ReplaceAll(family, "'", "''") + "'"
	for _, stmt := range []string{
		`CREATE TEMP VIEW check_runs AS SELECT ` + runCols + ` FROM main.check_runs WHERE family = ` + lit,
		`CREATE TEMP VIEW checks AS SELECT ` + checkCols + ` FROM main.checks WHERE run_id IN (SELECT id FROM main.check_runs WHERE family = ` + lit + `)`,
		`CREATE TEMP VIEW check_items AS SELECT ` + itemCols + ` FROM main.check_items WHERE check_id IN (SELECT id FROM main.checks WHERE run_id IN (SELECT id FROM main.check_runs WHERE family = ` + lit + `))`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("checkstore: scope to family: %w", err)
		}
	}
	return nil
}

func (s *store) Close() error {
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func (s *store) conn() (*sql.DB, error) {
	if s.db == nil {
		return nil, ErrClosed
	}
	return s.db, nil
}

// ensureAgentColumn adds check_runs.agent_id to a database an older engine created without it.
// Idempotent: a database that has the column is left as it is.
func ensureAgentColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(check_runs)`)
	if err != nil {
		return fmt.Errorf("checkstore: read check_runs columns: %w", err)
	}
	has := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("checkstore: read check_runs columns: %w", err)
		}
		has = has || name == "agent_id"
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if has {
		return nil
	}
	if _, err := db.Exec(`ALTER TABLE check_runs ADD COLUMN agent_id TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("checkstore: add check_runs.agent_id: %w", err)
	}
	return nil
}

// Import copies every run, check and item of the check-results database at src into dst,
// tagging each run with agentID when the source says none: the rows an older engine kept in a
// sub-agent's own database, brought into the family's. Idempotent (rows keep their ids and
// are ignored when they are already there), and src is only read: it is left in place,
// untouched. Returns how many runs were new to dst. dst must be a store opened by this package.
func Import(dst Store, src, agentID string) (int, error) {
	s, ok := dst.(*store)
	if !ok {
		return 0, fmt.Errorf("checkstore: Import needs a store opened by this package")
	}
	db, err := s.conn()
	if err != nil {
		return 0, err
	}
	if _, err := os.Stat(src); err != nil {
		return 0, err
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	attach := "ATTACH DATABASE ? AS src"
	if _, err := conn.ExecContext(context.Background(), attach, "file:"+url.PathEscape(src)+"?mode=ro"); err != nil {
		if _, err2 := conn.ExecContext(context.Background(), attach, src); err2 != nil {
			return 0, fmt.Errorf("checkstore: attach %s: %w", src, err)
		}
	}
	defer conn.ExecContext(context.Background(), "DETACH DATABASE src")

	hasAgent := false
	rows, err := conn.QueryContext(context.Background(), `PRAGMA src.table_info(check_runs)`)
	if err != nil {
		return 0, fmt.Errorf("checkstore: read %s: %w", src, err)
	}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err == nil && name == "agent_id" {
			hasAgent = true
		}
	}
	rows.Close()

	agentExpr := "?"
	args := []any{agentID}
	if hasAgent {
		agentExpr = "CASE WHEN agent_id = '' THEN ? ELSE agent_id END"
	}
	var before int
	_ = conn.QueryRowContext(context.Background(), `SELECT count(*) FROM main.check_runs`).Scan(&before)
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, err
	}
	stmts := []struct {
		sql  string
		args []any
	}{
		{`INSERT OR IGNORE INTO main.check_runs (id, run_batch_id, run_at, check_id, repo_id, branch, session_id, agent_id,
			base_ref, head_ref, exit_code, error, metadata, created_at)
			SELECT id, run_batch_id, run_at, check_id, repo_id, branch, session_id, ` + agentExpr + `,
			base_ref, head_ref, exit_code, error, metadata, created_at FROM src.check_runs`, args},
		{`INSERT OR IGNORE INTO main.checks (id, run_id, subject, kind, status, fingerprint, last_step, output, metadata, checked_at)
			SELECT id, run_id, subject, kind, status, fingerprint, last_step, output, metadata, checked_at FROM src.checks`, nil},
		{`INSERT OR IGNORE INTO main.check_items (id, check_id, key, passed, metadata, checked_at)
			SELECT id, check_id, key, passed, metadata, checked_at FROM src.check_items`, nil},
	}
	for _, st := range stmts {
		if _, err := tx.ExecContext(context.Background(), st.sql, st.args...); err != nil {
			_ = tx.Rollback()
			return 0, fmt.Errorf("checkstore: import %s: %w", src, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	var after int
	_ = conn.QueryRowContext(context.Background(), `SELECT count(*) FROM main.check_runs`).Scan(&after)
	return after - before, nil
}

// ensureSharedColumns adds what a database shared by every session of a repository needs on
// each run: check_runs.family (the root session of the session family) and check_runs.folder
// (the working tree). Idempotent.
func ensureSharedColumns(db *sql.DB) error {
	have := map[string]bool{}
	rows, err := db.Query(`PRAGMA table_info(check_runs)`)
	if err != nil {
		return fmt.Errorf("checkstore: read check_runs columns: %w", err)
	}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("checkstore: read check_runs columns: %w", err)
		}
		have[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, col := range []string{"family", "folder"} {
		if have[col] {
			continue
		}
		if _, err := db.Exec(`ALTER TABLE check_runs ADD COLUMN ` + col + ` TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("checkstore: add check_runs.%s: %w", col, err)
		}
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_check_runs_family ON check_runs(family, check_id, run_at)`); err != nil {
		return fmt.Errorf("checkstore: index family: %w", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS legacy_imports (path TEXT PRIMARY KEY, sig TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("checkstore: legacy_imports: %w", err)
	}
	return nil
}

// Legacy is a check-results database an older engine kept per session, to be brought into the
// repository's.
type Legacy struct {
	// Path is the old database file.
	Path string
	// Family is the root session its runs belong to; Agent tags runs that say none (a sub-agent's
	// own old database); Folder is the working tree they were judged in.
	Family, Agent, Folder string
}

// ImportLegacy copies each old database into the family store's file, once per change of the
// old file (its size and mtime are remembered in the store, so a file that is written to again
// is read again), leaving it where it is. Rows keep their ids and are ignored when already
// present, so it is safe to repeat and safe when two sessions do it at the same moment. A
// database that cannot be read is skipped and reported in the returned error; the rest are done.
func ImportLegacy(dst Store, sources []Legacy) error {
	s, ok := dst.(*store)
	if !ok || s.family == "" {
		return fmt.Errorf("checkstore: ImportLegacy needs a store opened by OpenFamily")
	}
	var errs []string
	for _, src := range sources {
		info, err := os.Stat(src.Path)
		if err != nil {
			continue
		}
		sig := fmt.Sprintf("%d-%d", info.Size(), info.ModTime().UnixNano())
		if wal, err := os.Stat(src.Path + "-wal"); err == nil {
			// Writes sit in the write-ahead log until a checkpoint: the main file alone can
			// look unchanged after new rows.
			sig += fmt.Sprintf("-%d-%d", wal.Size(), wal.ModTime().UnixNano())
		}
		var have string
		if err := s.db.QueryRow(`SELECT sig FROM main.legacy_imports WHERE path = ?`, src.Path).Scan(&have); err == nil && have == sig {
			continue
		}
		if err := s.importLegacy(src); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if _, err := s.db.Exec(`INSERT INTO main.legacy_imports (path, sig) VALUES (?, ?)
			ON CONFLICT(path) DO UPDATE SET sig = excluded.sig`, src.Path, sig); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("checkstore: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (s *store) importLegacy(src Legacy) error {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	attach := "ATTACH DATABASE ? AS src"
	if _, err := conn.ExecContext(ctx, attach, "file:"+url.PathEscape(src.Path)+"?mode=ro"); err != nil {
		if _, err2 := conn.ExecContext(ctx, attach, src.Path); err2 != nil {
			return fmt.Errorf("attach %s: %w", src.Path, err)
		}
	}
	defer conn.ExecContext(ctx, "DETACH DATABASE src")
	hasAgent := false
	rows, err := conn.QueryContext(ctx, `PRAGMA src.table_info(check_runs)`)
	if err != nil {
		return fmt.Errorf("read %s: %w", src.Path, err)
	}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err == nil && name == "agent_id" {
			hasAgent = true
		}
	}
	rows.Close()
	agentExpr := "?"
	if hasAgent {
		agentExpr = "CASE WHEN agent_id = '' THEN ? ELSE agent_id END"
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT OR IGNORE INTO main.check_runs (id, run_batch_id, run_at, check_id, repo_id, branch, session_id, agent_id,
			family, folder, base_ref, head_ref, exit_code, error, metadata, created_at)
			SELECT id, run_batch_id, run_at, check_id, repo_id, branch, session_id, ` + agentExpr + `,
			?, ?, base_ref, head_ref, exit_code, error, metadata, created_at FROM src.check_runs`, []any{src.Agent, src.Family, src.Folder}},
		{`INSERT OR IGNORE INTO main.checks (id, run_id, subject, kind, status, fingerprint, last_step, output, metadata, checked_at)
			SELECT id, run_id, subject, kind, status, fingerprint, last_step, output, metadata, checked_at FROM src.checks`, nil},
		{`INSERT OR IGNORE INTO main.check_items (id, check_id, key, passed, metadata, checked_at)
			SELECT id, check_id, key, passed, metadata, checked_at FROM src.check_items`, nil},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("import %s: %w", src.Path, err)
		}
	}
	return tx.Commit()
}
