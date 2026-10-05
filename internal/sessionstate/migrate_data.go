package sessionstate

import (
	"database/sql"
	"errors"
	"fmt"
)

// dataSteps are the migrations that change what a store HOLDS rather than its schema: the Go the
// engine runs, once per store, before a version's SQL is applied and recorded. Keyed by the
// migration's file name. Each step is idempotent — two hooks opening a store together may both run
// it, and a step interrupted before its version was recorded runs again — and does its heavy work
// without holding the write lock: a store that is 2 GB of one value must not stop every other hook
// of the session while it is rewritten.
var dataSteps = map[string]func(*sql.DB) error{
	"009_compact_citations.sql": compactStoredCitations,
}

// reclaimThreshold is the free space (bytes) a store must hold before it is compacted on disk: a
// VACUUM rewrites the file, and a store that shrank only a little is not worth it.
const reclaimThreshold = 16 << 20

// compactStoredCitations rewrites the citation history and the cycle record within their bounds
// and, when that frees a lot of space, compacts the file so a store that was gigabytes opens at its
// real size.
func compactStoredCitations(db *sql.DB) error {
	if err := swapCompacted(db, MetaCitations, CompactCitations); err != nil {
		return err
	}
	if err := swapCompacted(db, MetaCitedCycle, func(v string) (string, bool, error) {
		out, changed := CompactCycle(v)
		return out, changed, nil
	}); err != nil {
		return err
	}
	reclaimSpace(db)
	return nil
}

// swapCompacted replaces the value of key with its compacted form, only while it is still the value
// that was compacted: a hook of the live session writing it meanwhile makes the step read it again
// rather than overwrite the write. An absent or already-compact value is left alone.
func swapCompacted(db *sql.DB, key string, compact func(string) (string, bool, error)) error {
	for attempt := 0; attempt < 5; attempt++ {
		var old string
		err := db.QueryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&old)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("sessionstate: migrate %q: read: %w", key, err)
		}
		next, changed, err := compact(old)
		if err != nil {
			// A value this step cannot read is not this step's to repair: left as it is, and
			// every reader already treats an unparseable one as empty.
			return nil
		}
		if !changed {
			return nil
		}
		res, err := db.Exec("UPDATE meta SET value = ? WHERE key = ? AND value = ?", next, key, old)
		if err != nil {
			return fmt.Errorf("sessionstate: migrate %q: write: %w", key, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return nil
		}
	}
	return nil // the live session keeps rewriting it; the next open compacts what is left
}

// reclaimSpace gives the file back what compaction freed. Best effort: a busy store is compacted
// by the next open that finds it free.
func reclaimSpace(db *sql.DB) {
	_, _ = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	var free, size int64
	if db.QueryRow("PRAGMA freelist_count").Scan(&free) != nil || db.QueryRow("PRAGMA page_size").Scan(&size) != nil {
		return
	}
	if free*size < reclaimThreshold {
		return
	}
	_, _ = db.Exec("VACUUM")
	_, _ = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
}
