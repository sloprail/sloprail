package sessionstate

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrate applies every migration the binary carries that this database has not
// seen, in filename order.
//
// Applied migrations are counted rather than named, in SQLite's own
// user_version. A migration file is never edited once written — a database in
// the field has already run the old text, and rewriting it would leave that
// database describing a schema it does not have, with nothing to notice.
func migrate(db *sql.DB) error {
	files, err := migrationFiles()
	if err != nil {
		return err
	}

	for {
		version, err := schemaVersion(db)
		if err != nil {
			return err
		}
		if version > len(files) {
			// The database was written by a newer binary. Refusing is the
			// honest answer: an older binary cannot know which columns it is
			// missing, and running against a schema it does not understand
			// corrupts a session's record rather than failing it.
			return fmt.Errorf("%w: database is version %d, this binary knows %d",
				ErrSchemaTooNew, version, len(files))
		}
		if version == len(files) {
			return nil
		}

		body, err := migrationFS.ReadFile("migrations/" + files[version])
		if err != nil {
			return fmt.Errorf("sessionstate: read migration %s: %w", files[version], err)
		}
		if step := dataSteps[files[version]]; step != nil {
			// What the store holds, rewritten before the version that says it was is recorded.
			if err := step(db); err != nil {
				return fmt.Errorf("sessionstate: migrate %s: %w", files[version], err)
			}
		}
		if err := applyMigration(db, files[version], string(body), version+1); err != nil {
			return err
		}
	}
}

// schemaVersion reads how many migrations this database has had applied.
//
// Retried on a busy database, which busy_timeout alone does not cover here.
// Several hook processes starting at once all open a database that does not
// exist yet, and the first to get there converts it to WAL under an exclusive
// lock. That conversion happens as the driver initialises the connection —
// before the connection's own busy_timeout is in force — so a contending opener
// is refused outright rather than made to wait. It is transient by
// construction: the conversion happens once and takes microseconds, so what the
// retry waits for has already nearly finished.
func schemaVersion(db *sql.DB) (int, error) {
	var lastErr error
	for attempt := range busyRetries {
		tx, err := db.Begin()
		if err == nil {
			var version int
			err = tx.QueryRow("PRAGMA user_version").Scan(&version)
			tx.Rollback()
			if err == nil {
				return version, nil
			}
		}
		if !isBusy(err) {
			return 0, fmt.Errorf("sessionstate: read schema version: %w", err)
		}
		lastErr = err
		// Linear backoff. The wait is for one short exclusive lock, not for a
		// queue, so spreading attempts out is enough and doubling would mostly
		// add latency to the case that already resolved.
		time.Sleep(time.Duration(attempt+1) * busyRetryStep)
	}
	return 0, fmt.Errorf("sessionstate: read schema version: %w", lastErr)
}

const (
	// busyRetries and busyRetryStep bound the wait at a little over a second in
	// total — far beyond the microseconds a WAL conversion needs, and short
	// enough that a genuinely stuck database still surfaces as an error rather
	// than a hung hook.
	busyRetries   = 20
	busyRetryStep = 5 * time.Millisecond
)

// isBusy reports whether err is SQLite refusing because someone else holds the
// lock. Matched on the driver's own code rather than on message text, which is
// what the sentinel rule is protecting against.
func isBusy(err error) bool {
	if err == nil {
		return false
	}
	var serr *sqlite.Error
	if errors.As(err, &serr) {
		code := serr.Code()
		return code == sqlite3.SQLITE_BUSY || code == sqlite3.SQLITE_LOCKED
	}
	return false
}

// applyMigration runs one migration and its version bump in a single
// transaction, so an interrupted upgrade leaves the database on the version it
// actually has rather than one it half-reached.
//
// The version is re-read INSIDE the transaction, and the migration is skipped
// if another writer got there first. Two hook processes starting together both
// see version 0 outside a transaction and both try to create the same tables;
// without this the loser dies on "table meta already exists" — a hook failing
// at startup, which a harness reads as a refusal. BEGIN IMMEDIATE takes the
// write lock up front so the re-read cannot be overtaken between the check and
// the CREATE.
func applyMigration(db *sql.DB, name, body string, version int) error {
	// The write lock is held from the very start of this transaction, not from
	// its first write — see _txlock=immediate in the DSN. A deferred BEGIN,
	// which is the default, would leave the re-read below racing exactly the
	// way the unguarded read outside did.
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("sessionstate: begin migration %s: %w", name, err)
	}
	defer tx.Rollback()

	var current int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("sessionstate: read schema version: %w", err)
	}
	if current >= version {
		// Another process applied it while we were waiting for the lock. Its
		// work is this work, so there is nothing to do and nothing wrong.
		return nil
	}

	if _, err := tx.Exec(body); err != nil {
		return fmt.Errorf("sessionstate: apply migration %s: %w", name, err)
	}
	// PRAGMA takes no parameters, so the value is formatted in. It is an int
	// derived from a count of embedded files, never from input.
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return fmt.Errorf("sessionstate: record migration %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sessionstate: commit migration %s: %w", name, err)
	}
	return nil
}

// migrationFiles lists the embedded migrations in the order they apply, which
// is the order their numeric prefixes sort in.
func migrationFiles() ([]string, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("sessionstate: read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
