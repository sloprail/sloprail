package checkstore

// Everything about the per-session databases an older engine kept (one checks.db per session):
// bringing them into the repository's database, reading them beside it until they are in, and
// telling whether a Stop of an older engine is writing one right now.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ImportLegacy copies each old database into the family store's file, once per change of the
// old file (its size and mtime are remembered in the store, so a file that is written to again
// is read again), leaving it where it is. Rows keep their ids and are ignored when already
// present, so it is safe to repeat and safe when two sessions do it at the same moment. A
// database that cannot be read is skipped and reported in the returned error; the rest are done.
func ImportLegacy(dst Store, sources []Legacy) error {
	_, err := ImportLegacyReport(dst, sources)
	return err
}

// RunningWindow is the longest a Stop can run (the hook's timeout): a run recorded RUNNING
// longer ago than this is a crashed one, whatever else is said about it.
//
// RESIDUAL WINDOW: an engine older than the Stop lock and the owner record writes neither, so
// for ITS runs "a Stop is still evaluating" can only be inferred from a RUNNING row younger
// than this window. A crashed old Stop therefore postpones migration of its file for up to
// RunningWindow; a live one is never migrated under. Nothing is ever lost by waiting: the
// cycle keeps using the old file as it is.
const RunningWindow = 3600 * time.Second

// Legacy is a check-results database an older engine kept per session, to be brought into the
// repository's.
type Legacy struct {
	// Path is the old database file.
	Path string
	// Family is the root session its runs belong to; Agent tags runs that say none (a sub-agent's
	// own old database); Folder is the working tree they were judged in.
	Family, Agent, Folder string
}

var errPostponed = errors.New("checkstore: an older engine is still writing this database")

func fileSig(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	sig := fmt.Sprintf("%d-%d", info.Size(), info.ModTime().UnixNano())
	if wal, err := os.Stat(path + "-wal"); err == nil {
		// Writes sit in the write-ahead log until a checkpoint: the main file alone can look
		// unchanged after new rows.
		sig += fmt.Sprintf("-%d-%d", wal.Size(), wal.ModTime().UnixNano())
	}
	return sig, true
}

// lockPathFor is the Stop lock of one old database: in the repository database's directory,
// keyed by the old file's path, so the old files and directories are never touched.
func lockPathFor(repoPath, src string) string {
	sum := sha256.Sum256([]byte(src))
	return filepath.Join(filepath.Dir(repoPath), "locks", hex.EncodeToString(sum[:])[:16]+".stop-lock")
}

// Imported reports whether the repository database at repoPath holds everything the old file
// held when it was last looked at: its signature is the one recorded at import. Read-only.
func Imported(repoPath, src string) bool {
	sig, ok := fileSig(src)
	if !ok {
		return true
	}
	if _, err := os.Stat(repoPath); err != nil {
		return false
	}
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(repoPath)+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return false
	}
	defer db.Close()
	var have string
	if err := db.QueryRow(`SELECT sig FROM legacy_imports WHERE path = ?`, src).Scan(&have); err != nil {
		return false
	}
	return have == sig
}

// ImportLegacyReport is ImportLegacy that also returns the sources it POSTPONED: a source a
// Stop is evaluating right now (it holds the source's lock, or an older engine's RUNNING run sits
// in it) is not migrated while that goes on — the caller keeps using it as it is for the cycle
// (OpenLegacy) and the next hook imports it. Nothing is postponed forever: a late write, by an
// older binary that was still running when this one replaced it, changes the file's signature
// and is picked up (new rows, and a run that has since finished) at the next hook.
func ImportLegacyReport(dst Store, sources []Legacy) ([]string, error) {
	s, ok := dst.(*store)
	if !ok || s.family == "" {
		return nil, fmt.Errorf("checkstore: ImportLegacy needs a store opened by OpenFamily")
	}
	var errs, postponed []string
	for _, src := range sources {
		sig, ok := fileSig(src.Path)
		if !ok {
			continue
		}
		var have string
		if err := s.db.QueryRow(`SELECT sig FROM main.legacy_imports WHERE path = ?`, src.Path).Scan(&have); err == nil && have == sig {
			continue
		}
		if err := s.importLegacy(src); err != nil {
			if errors.Is(err, errPostponed) {
				postponed = append(postponed, src.Path)
				continue
			}
			errs = append(errs, err.Error())
			continue
		}
		if _, err := s.db.Exec(`INSERT INTO main.legacy_imports (path, sig) VALUES (?, ?)
			ON CONFLICT(path) DO UPDATE SET sig = excluded.sig`, src.Path, sig); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return postponed, fmt.Errorf("checkstore: %s", strings.Join(errs, "; "))
	}
	return postponed, nil
}

func attachSrc(ctx context.Context, conn *sql.Conn, path, as string) error {
	attach := "ATTACH DATABASE ? AS " + as
	if _, err := conn.ExecContext(ctx, attach, "file:"+url.PathEscape(path)+"?mode=ro"); err != nil {
		if _, err2 := conn.ExecContext(ctx, attach, path); err2 != nil {
			return fmt.Errorf("attach %s: %w", path, err)
		}
	}
	return nil
}

// runningBlocks says a run recorded RUNNING in the attached database `src` may still be being
// written: it began within RunningWindow, and its owner (a new engine records pid and host in
// run_owners) is not provably gone. A run without an owner record is an older engine's: only
// its age can tell.
func runningBlocks(ctx context.Context, conn *sql.Conn, src string) (bool, error) {
	cutoff := time.Now().Add(-RunningWindow).UTC().Format("2006-01-02T15:04:05.000000000Z")
	rows, err := conn.QueryContext(ctx, `SELECT id FROM `+src+`.check_runs
		WHERE json_extract(metadata, '$.state') = 'running' AND run_at > ?`, cutoff)
	if err != nil {
		return false, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return false, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	host, _ := os.Hostname()
	for _, id := range ids {
		var pid int
		var h string
		err := conn.QueryRowContext(ctx, `SELECT pid, host FROM `+src+`.run_owners WHERE run_id = ?`, id).Scan(&pid, &h)
		if err == nil && h == host && processGone(pid) {
			continue // its process is gone: a crashed Stop
		}
		return true, nil
	}
	return false, nil
}

type stmt struct {
	sql  string
	args []any
}

// copyStatements brings src's rows into main. withFamily: main is the repository's database
// (runs carry family and folder); otherwise it is a plain per-session layout.
//
// A run already in is updated only while it is still RUNNING there: an older binary finishing it
// after the first copy is the late write to pick up; a finished run is final. A check is updated
// when src has the newer verdict (a resolution made in main stamps its own later time, which then
// stands, so a refusal resolved there never comes back from the old file). A check re-recorded by
// the old engine got NEW item ids: the items of every check src has a newer verdict for are
// replaced, never left beside the stale ones.
func copyStatements(withFamily, hasAgent bool, l Legacy) []stmt {
	agentExpr, famCols, famVals := "?", "", ""
	args := []any{l.Agent}
	if hasAgent {
		agentExpr = "CASE WHEN agent_id = '' THEN ? ELSE agent_id END"
	}
	if withFamily {
		famCols, famVals = "family, folder, ", "?, ?, "
		args = append(args, l.Family, l.Folder)
	}
	return []stmt{
		{`INSERT INTO main.check_runs (id, run_batch_id, run_at, check_id, repo_id, branch, session_id, agent_id,
			` + famCols + `base_ref, head_ref, exit_code, error, metadata, created_at)
			SELECT id, run_batch_id, run_at, check_id, repo_id, branch, session_id, ` + agentExpr + `,
			` + famVals + `base_ref, head_ref, exit_code, error, metadata, created_at FROM src.check_runs WHERE true
			ON CONFLICT(id) DO UPDATE SET exit_code = excluded.exit_code, error = excluded.error, metadata = excluded.metadata
			WHERE json_extract(check_runs.metadata, '$.state') = 'running'`, args},
		{`DELETE FROM main.check_items WHERE check_id IN (
			SELECT m.id FROM main.checks m JOIN src.checks s ON s.id = m.id WHERE s.checked_at > m.checked_at)`, nil},
		{`INSERT INTO main.checks (id, run_id, subject, kind, status, fingerprint, last_step, output, metadata, checked_at)
			SELECT id, run_id, subject, kind, status, fingerprint, last_step, output, metadata, checked_at FROM src.checks WHERE true
			ON CONFLICT(id) DO UPDATE SET status = excluded.status, fingerprint = excluded.fingerprint,
			    output = excluded.output, metadata = excluded.metadata, checked_at = excluded.checked_at
			WHERE excluded.checked_at > checks.checked_at`, nil},
		{`INSERT OR IGNORE INTO main.check_items (id, check_id, key, passed, metadata, checked_at)
			SELECT id, check_id, key, passed, metadata, checked_at FROM src.check_items`, nil},
	}
}

func srcHasAgent(ctx context.Context, conn *sql.Conn) bool {
	rows, err := conn.QueryContext(ctx, `PRAGMA src.table_info(check_runs)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	has := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err == nil && name == "agent_id" {
			has = true
		}
	}
	return has
}

func (s *store) importLegacy(src Legacy) error {
	// Nothing is migrated while a Stop is evaluating the source: it takes the lock shared for
	// its whole run (the kernel drops it if the process dies), we take it exclusive without
	// waiting. Held means a Stop is running: postpone. A lock that cannot be made at all says
	// nothing about a Stop, and only the RUNNING-row check below decides.
	release, state := tryExclusive(lockPathFor(s.path, src.Path))
	if state == lockHeld {
		return errPostponed
	}
	defer release()

	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := attachSrc(ctx, conn, src.Path, "src"); err != nil {
		return err
	}
	defer conn.ExecContext(ctx, "DETACH DATABASE src")
	// One writer at a time across processes: concurrent hooks wait here (busy_timeout), the
	// first imports, the rest find the rows already in.
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return err
	}
	rollback := func() { _, _ = conn.ExecContext(ctx, `ROLLBACK`) }
	if blocked, err := runningBlocks(ctx, conn, "src"); err == nil && blocked {
		rollback()
		return errPostponed
	}
	for _, q := range copyStatements(true, srcHasAgent(ctx, conn), src) {
		if _, err := conn.ExecContext(ctx, q.sql, q.args...); err != nil {
			rollback()
			return fmt.Errorf("import %s: %w", src.Path, err)
		}
	}
	_, err = conn.ExecContext(ctx, `COMMIT`)
	return err
}

// materializeFamily copies one family's rows from the shared file into tables of the
// connection's own (in-memory) database, in ONE read transaction on the attached file so the
// three tables are one snapshot, then detaches the file.
func materializeFamily(conn *sql.Conn, path, family string) error {
	ctx := context.Background()
	if err := attachSrc(ctx, conn, path, "fam"); err != nil {
		return fmt.Errorf("checkstore: query: %w", err)
	}
	defer conn.ExecContext(ctx, `DETACH DATABASE fam`)
	return copyFamilyRows(ctx, conn, family)
}

func copyFamilyRows(ctx context.Context, conn *sql.Conn, family string) error {
	lit := "'" + strings.ReplaceAll(family, "'", "''") + "'"
	cols := func(c string) string { return strings.ReplaceAll(c, ", rowid AS rowid", "") }
	if _, err := conn.ExecContext(ctx, `BEGIN`); err != nil {
		return fmt.Errorf("checkstore: query: %w", err)
	}
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS check_runs AS SELECT ` + cols(runCols) + ` FROM fam.check_runs WHERE 0`,
		`CREATE TABLE IF NOT EXISTS checks AS SELECT ` + cols(checkCols) + ` FROM fam.checks WHERE 0`,
		`CREATE TABLE IF NOT EXISTS check_items AS SELECT ` + cols(itemCols) + ` FROM fam.check_items WHERE 0`,
		`INSERT INTO main.check_runs SELECT ` + cols(runCols) + ` FROM fam.check_runs WHERE family = ` + lit + ` ORDER BY rowid`,
		`INSERT INTO main.checks SELECT ` + cols(checkCols) + ` FROM fam.checks WHERE run_id IN (SELECT id FROM main.check_runs) ORDER BY rowid`,
		`INSERT INTO main.check_items SELECT ` + cols(itemCols) + ` FROM fam.check_items WHERE check_id IN (SELECT id FROM main.checks) ORDER BY rowid`,
	} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
			return fmt.Errorf("checkstore: query: %w", err)
		}
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("checkstore: query: %w", err)
	}
	return nil
}

// OpenUnionReadOnly is a family's results for a READER while some of its old per-session files
// are not fully in the repository database yet: an in-memory store holding the repository's rows
// for the family (when the database exists) and, beside them, every old file's rows (the
// repository's row wins on the same id, so a refusal resolved there stays resolved; a run the
// repository holds RUNNING takes the old file's later finish). Nothing is written anywhere, no
// lock is taken, nothing is imported.
func OpenUnionReadOnly(repoPath, family string, old []Legacy) (Store, error) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("checkstore: open union: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("checkstore: apply schema: %w", err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, err
	}
	fail := func(err error) (Store, error) {
		conn.Close()
		db.Close()
		return nil, err
	}
	if _, statErr := os.Stat(repoPath); statErr == nil {
		if err := attachSrc(ctx, conn, repoPath, "fam"); err != nil {
			return fail(err)
		}
		lit := "'" + strings.ReplaceAll(family, "'", "''") + "'"
		cols := func(c string) string { return strings.ReplaceAll(c, ", rowid AS rowid", "") }
		for _, q := range []string{
			`INSERT INTO main.check_runs SELECT ` + cols(runCols) + ` FROM fam.check_runs WHERE family = ` + lit + ` ORDER BY rowid`,
			`INSERT INTO main.checks SELECT ` + cols(checkCols) + ` FROM fam.checks WHERE run_id IN (SELECT id FROM main.check_runs) ORDER BY rowid`,
			`INSERT INTO main.check_items SELECT ` + cols(itemCols) + ` FROM fam.check_items WHERE check_id IN (SELECT id FROM main.checks) ORDER BY rowid`,
		} {
			if _, err := conn.ExecContext(ctx, q); err != nil {
				return fail(fmt.Errorf("checkstore: read %s: %w", repoPath, err))
			}
		}
		if _, err := conn.ExecContext(ctx, `DETACH DATABASE fam`); err != nil {
			return fail(err)
		}
	}
	for _, l := range old {
		if _, err := os.Stat(l.Path); err != nil {
			continue
		}
		if err := attachSrc(ctx, conn, l.Path, "src"); err != nil {
			return fail(err)
		}
		for _, q := range copyStatements(false, srcHasAgent(ctx, conn), l) {
			if _, err := conn.ExecContext(ctx, q.sql, q.args...); err != nil {
				return fail(fmt.Errorf("checkstore: read %s: %w", l.Path, err))
			}
		}
		if _, err := conn.ExecContext(ctx, `DETACH DATABASE src`); err != nil {
			return fail(err)
		}
	}
	conn.Close()
	return &store{db: db, path: repoPath}, nil
}

// StopLock is held (shared) by a Stop that evaluates an old per-session database, so nothing
// migrates the file from under it.

// OpenLegacy opens an old per-session database as it is, read-write, for the cycle in which it
// could not be migrated yet. It holds the Stop lock until Close. repoPath and family let the
// other sessions' results in the repository database be read beside it (SiblingRunRefs).
func OpenLegacy(path, repoPath, family string) (Store, error) {
	release := lockShared(lockPathFor(repoPath, path))
	st, err := Open(path)
	if err != nil {
		release()
		return nil, err
	}
	return &lockedStore{Store: st, release: release, repoPath: repoPath, family: family}, nil
}

type lockedStore struct {
	Store
	release  func()
	repoPath string
	family   string
}

func (l *lockedStore) Close() error {
	err := l.Store.Close()
	l.release()
	return err
}

// SiblingRunRefs reads the other families' runs of the same folder from the repository
// database (the old files of the other sessions are the caller's to add).
func (l *lockedStore) SiblingRunRefs(rule, folder string) (RunRefs, error) {
	if l.repoPath == "" {
		return RunRefs{}, nil
	}
	ro, err := OpenFamilyReadOnly(l.repoPath, l.family)
	if errors.Is(err, ErrNoStore) {
		return RunRefs{}, nil
	}
	if err != nil {
		return RunRefs{}, err
	}
	defer ro.Close()
	return ro.(SiblingRefs).SiblingRunRefs(rule, folder)
}

// LegacyBusy says an older database is being written right now: a Stop holds its lock, or (an
// older engine takes no lock) a run recorded RUNNING sits in it whose owner is not provably gone.
func LegacyBusy(path, repoPath string) bool {
	release, state := tryExclusive(lockPathFor(repoPath, path))
	release()
	if state == lockHeld {
		return true
	}
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return false
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		return false
	}
	defer conn.Close()
	// runningBlocks names its source schema: main is the file itself here.
	blocked, err := runningBlocks(ctx, conn, "main")
	return err == nil && blocked
}
