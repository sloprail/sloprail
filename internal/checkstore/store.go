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
// One database per session today, checks.db beside the session's state.db. The
// identity columns on every run (repo, branch, session) are what let the same
// rows move to one machine-wide database later without a change of shape.
//
// This is the only package that opens the file. The writer (the Stop
// evaluation) opens it read-write; a reader (`sr-checks`, `sr-session
// changeset`) opens it with OpenReadOnly, which cannot create it, migrate it or
// write to it — reading what a session concluded must never be able to change it.
package checkstore

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

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
	// RecordCheck stores one check of a run — replacing the same (subject, kind)
	// of that run — with its findings, and returns the check's id.
	RecordCheck(runID string, c CheckRecord) (string, error)
	// CachedCheck finds a stored pass or fail for (subject, kind, fingerprint):
	// the most recent one. A fail is returned like a pass — it is terminal and is
	// replayed, never re-judged until the input changes. An empty fingerprint
	// (a script) never hits.
	CachedCheck(subject, kind, fingerprint string) (CachedCheck, bool, error)
	// ResolveStale marks as skip every failing check of rule (at this rule hash)
	// outside run liveRunID whose (subject, kind, fingerprint) is not one liveRunID
	// holds: a failure whose input has left the range. Returns how many.
	ResolveStale(rule, ruleHash, liveRunID string) (int, error)
	// PassedHeads lists, newest first, the head_ref of each of the rule's runs at
	// this rule hash that passed: no engine error and no failing check. The
	// caller picks the first still reachable — that is the rule's watermark.
	PassedHeads(rule, ruleHash string) ([]string, error)
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
	db *sql.DB
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
	return &store{db: db}, nil
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
	return &store{db: db}, nil
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
