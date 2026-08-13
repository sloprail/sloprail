// Package sessionstate holds what one session must remember between hook runs.
//
// Three things live here, and they are together because they share a lifetime
// and a location: where the session started, which content each guardrail has
// already judged, and whatever a rule spanning more than one cycle needs to
// carry forward. All of it dies with the session, and all of it is keyed by it.
//
// This is the only package that imports a database driver. Callers get their
// own types back and never learn that any of this is SQL.
package sessionstate

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	// The database is this package's resource: nothing else opens it, so
	// nothing else needs the driver. Registered by this import; migrate.go also
	// imports it by name, to recognise a busy database by the driver's own code
	// rather than by its message text.
	_ "modernc.org/sqlite"
)

// ErrClosed is returned by every method once the store has been closed. A
// sentinel rather than the driver's own error, so a caller can recognise it
// without knowing which driver produced it.
var ErrClosed = errors.New("sessionstate: store is closed")

// ErrSchemaTooNew reports a database written by a newer binary than this one.
//
// A sentinel because a caller has a real decision to make on it — telling the
// user to update rather than reporting a broken session — and matching that on
// a substring of a formatted message would break the first time the wording
// changed.
var ErrSchemaTooNew = errors.New("sessionstate: database schema is newer than this binary")

// Store is one session's memory.
//
// Callers name what they want — a meta key, a file and a guardrail, a state
// entry — and never a table, a column, or a path. What it is stored in is this
// package's business, and the interface is what keeps it that way.
type Store interface {
	// Meta reads a session fact. A key never written is "", false — absent is
	// an answer, not a failure.
	Meta(key string) (string, bool, error)
	// SetMeta records a session fact, replacing any previous value.
	SetMeta(key, value string) error

	// FileCheck reads one guardrail's verdict on one file. A check never
	// recorded is a zero verdict and false.
	FileCheck(path, guardrail string) (Verdict, bool, error)
	// RecordFileCheck stores a verdict, replacing whatever the same guardrail
	// last said about the same file.
	RecordFileCheck(path, guardrail string, v Verdict) error
	// Skippable reports whether a guardrail may skip a file holding the given
	// content, which is true only of content it has already permitted.
	Skippable(path, guardrail, fingerprint string) (bool, error)

	// State reads what one guardrail stored under one key. A key never written
	// is "", false: a rule asking whether it has seen something before should
	// not have to tell "no" apart from "broken".
	State(guardrail, key string) (string, bool, error)
	// SetState stores a value under a key, replacing rather than merging.
	SetState(guardrail, key, value string) error
	// ListState returns every entry this guardrail stored whose key begins
	// with prefix, ordered by key. An empty prefix is everything it stored.
	ListState(guardrail, prefix string) ([]Entry, error)

	// Close releases the database.
	Close() error
}

// Verdict is what one guardrail concluded about one file's content.
//
// The fingerprint travels with the verdict because neither is usable alone: a
// pass is only a licence to skip while the content it was given still yields
// the same fingerprint.
type Verdict struct {
	// Fingerprint of the content that was judged.
	Fingerprint string
	// Passed reports whether the guardrail permitted the content. A refusal is
	// kept rather than dropped, so the violation resurfaces every cycle until
	// the content changes or the check passes.
	Passed bool
}

// Entry is one guardrail-state row as the rule that wrote it sees it: its own
// key and its own value, with the guardrail left out because a rule reading its
// own entries already knows whose they are.
type Entry struct {
	Key   string
	Value string
}

// store is the only implementation. Unexported so the interface stays the whole
// of what callers can depend on.
type store struct {
	db *sql.DB
}

// Open opens the session database at path, creating it and its parent
// directories if they are not there yet, and brings the schema up to date.
//
// The path is given, never derived: where a session's state belongs depends on
// the platform's data directory and on the session's identity, and both are the
// caller's to know. A store that resolved its own root could not be pointed at
// a temporary directory, which is most of what makes it testable.
func Open(path string) (Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("sessionstate: mkdir %s: %w", filepath.Dir(path), err)
	}
	return open(path)
}

// pragmas are applied to every connection as it opens, by putting them in the
// DSN rather than running them afterwards.
//
// The difference matters. database/sql hands each statement whichever pooled
// connection is free, so a pragma sent through Exec configures one connection
// and says nothing about the next — and busy_timeout is per connection. Sent
// that way, the timeout can be absent from the very connection that then hits
// contention, which is how the first attempt at this still failed with
// SQLITE_BUSY on the WAL switch itself.
//
// Order within the DSN is still deliberate: _pragma is applied in sequence, and
// the timeout has to exist before the WAL switch, which takes an exclusive lock
// and is the first thing two simultaneous openers contend on.
var pragmas = []string{
	// What makes a losing writer wait rather than fail. The contending writers
	// here are separate hook PROCESSES, which no in-process connection limit
	// reaches, and SQLite's own answer to a second writer is to fail it at once
	// with SQLITE_BUSY — a non-zero exit, which a harness reads as a refusal of
	// the agent's work. Five seconds is far longer than any write here takes
	// and still bounded, so a genuinely stuck database surfaces rather than
	// hanging a hook forever.
	"busy_timeout(5000)",
	// A reader no longer blocks a writer, so one hook reading its state while
	// another records a verdict stops contending at all.
	"journal_mode(WAL)",
	// Durability at commit rather than at every write. WAL's own crash
	// guarantee is what this store needs, and the stricter setting costs a disk
	// sync per verdict for a database that is rebuilt if it is ever lost.
	"synchronous(NORMAL)",
	"foreign_keys(ON)",
}

// open is Open without the directory, which is also what the tests use against
// an in-memory database — one that has no directory to make.
func open(path string) (*store, error) {
	// _txlock=immediate takes the write lock when a transaction BEGINs rather
	// than at its first write. Migration reads the schema version and acts on
	// it in one transaction, and a deferred lock would let another process slip
	// between the two.
	dsn := path + "?_txlock=immediate&" + pragmaQuery()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sessionstate: open %s: %w", path, err)
	}
	// Serialises this process's own writers. The cross-process half of the
	// problem is busy_timeout's, above.
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &store{db: db}, nil
}

// pragmaQuery renders the pragmas as the driver's DSN parameters.
func pragmaQuery() string {
	parts := make([]string, 0, len(pragmas))
	for _, p := range pragmas {
		parts = append(parts, "_pragma="+url.QueryEscape(p))
	}
	return strings.Join(parts, "&")
}

func (s *store) Close() error {
	if s.db == nil {
		return nil
	}
	db := s.db
	s.db = nil
	if err := db.Close(); err != nil {
		return fmt.Errorf("sessionstate: close: %w", err)
	}
	return nil
}

// conn returns the handle, or ErrClosed. Every method goes through it so a use
// after close is a named error rather than a nil dereference.
func (s *store) conn() (*sql.DB, error) {
	if s.db == nil {
		return nil, ErrClosed
	}
	return s.db, nil
}
