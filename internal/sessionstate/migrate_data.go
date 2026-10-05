package sessionstate

import (
	"database/sql"
	"errors"
	"fmt"
	"unicode/utf8"
)

// dataSteps are the migrations that change what a store HOLDS rather than its schema: the Go the
// engine runs, once per store, before a version's SQL is applied and recorded. Keyed by the
// migration's file name. Each step is idempotent — two hooks opening a store together may both run
// it, and a step interrupted before its version was recorded runs again — and does its heavy work
// without holding the write lock: a store that is 2 GB of one value must not stop every other hook
// of the session while it is rewritten.
var dataSteps = map[string]func(*sql.DB) error{
	"009_compact_citations.sql": compactStoredCitations,
	"010_prune_gone.sql":        pruneRegistry,
}

// reclaimThreshold is the free space (bytes) a store must hold before it is compacted on disk: a
// VACUUM rewrites the file, and a store that shrank only a little is not worth it.
const reclaimThreshold = 16 << 20

// bloatedCitations is the size (bytes) beyond which a store's citation history is compacted whatever
// version it is at: a store left uncompacted by a step that lost a race with the live session's own
// writes, or written by a build without the bound, is repaired by the next open instead of staying
// huge for good.
const bloatedCitations = 16 << 20

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

// maintain is what every open does after the schema is current: a history grown past what the
// bound allows is compacted, and a file holding a lot of free space gives it back. Best effort — a
// busy store is repaired by a later open — and cheap when there is nothing to do (a length and two
// pragmas).
func maintain(db *sql.DB) {
	var n sql.NullInt64
	if db.QueryRow("SELECT length(value) FROM meta WHERE key = ?", MetaCitations).Scan(&n) == nil && n.Valid && n.Int64 > bloatedCitations {
		_ = swapCompacted(db, MetaCitations, CompactCitations)
	}
	reclaimIfFree(db)
}

// swapCompacted replaces the value of key with its compacted form, only while it is still the value
// that was compacted: a hook of the live session writing it meanwhile makes the step read it again
// rather than overwrite the write. An absent or already-compact value is left alone. A busy store
// is left for a later open (maintain).
func swapCompacted(db *sql.DB, key string, compact func(string) (string, bool, error)) error {
	for attempt := 0; attempt < 5; attempt++ {
		var old string
		err := db.QueryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&old)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			if isBusy(err) {
				return nil
			}
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
		// Still the value that was read: its length and both ends, rather than all of it again (it
		// may be a gigabyte, and the comparison is made inside the write).
		res, err := db.Exec(`UPDATE meta SET value = ? WHERE key = ? AND length(value) = ?
			AND substr(value, 1, 4096) = ? AND substr(value, -4096) = ?`,
			next, key, utf8.RuneCountInString(old), headOf(old), tailOf(old))
		if err != nil {
			if isBusy(err) {
				return nil
			}
			return fmt.Errorf("sessionstate: migrate %q: write: %w", key, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return nil
		}
	}
	return nil // the live session keeps rewriting it; a later open compacts what is left
}

// headOf and tailOf are the first and last 4096 characters of s, as SQLite's substr counts them.
func headOf(s string) string {
	i := 0
	for n := 0; n < 4096 && i < len(s); n++ {
		_, w := utf8.DecodeRuneInString(s[i:])
		i += w
	}
	return s[:i]
}

func tailOf(s string) string {
	i := len(s)
	for n := 0; n < 4096 && i > 0; n++ {
		_, w := utf8.DecodeLastRuneInString(s[:i])
		i -= w
	}
	return s[i:]
}

// reclaimSpace gives the file back what compaction freed. Best effort: a busy store is compacted
// by the next open that finds it holding free space (reclaimIfFree).
func reclaimSpace(db *sql.DB) {
	_, _ = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	reclaimIfFree(db)
}

func reclaimIfFree(db *sql.DB) {
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
