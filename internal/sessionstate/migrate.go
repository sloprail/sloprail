package sessionstate

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
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

	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("sessionstate: read schema version: %w", err)
	}
	if version > len(files) {
		// The database was written by a newer binary. Refusing is the honest
		// answer: an older binary cannot know which columns it is missing, and
		// running against a schema it does not understand corrupts a session's
		// record rather than failing it.
		return fmt.Errorf("sessionstate: database schema is version %d, this binary knows %d", version, len(files))
	}

	for i := version; i < len(files); i++ {
		body, err := migrationFS.ReadFile("migrations/" + files[i])
		if err != nil {
			return fmt.Errorf("sessionstate: read migration %s: %w", files[i], err)
		}
		if err := applyMigration(db, files[i], string(body), i+1); err != nil {
			return err
		}
	}
	return nil
}

// applyMigration runs one migration and its version bump in a single
// transaction, so an interrupted upgrade leaves the database on the version it
// actually has rather than one it half-reached.
func applyMigration(db *sql.DB, name, body string, version int) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("sessionstate: begin migration %s: %w", name, err)
	}
	defer tx.Rollback()

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
