package sessionstate

import (
	"database/sql"
	"errors"
	"fmt"
)

// Keys the engine itself writes. A rule's keys are its own; these are the
// engine's, named here so the two commands that use one agree on its spelling.
const (
	// MetaBaselineCommit is where a cycle measures its difference from.
	MetaBaselineCommit = "baseline_commit"
	// MetaBaselineBranch is which line of history that point belongs to. Kept
	// beside the commit because an agent may switch branches mid-session, and a
	// point recorded on the line it left describes a history the tree no longer
	// has.
	MetaBaselineBranch = "baseline_branch"
	// MetaTranscriptRead is how far the session's own record has been read. A
	// position rather than a state, because a record only grows.
	MetaTranscriptRead = "transcript_read"

	// MetaTranscriptOffered is how far the record had been read out to something
	// that could judge it, before any cycle confirmed having judged it.
	//
	// Separate from MetaTranscriptRead because the two answer different
	// questions and the gap between them is where a turn goes missing. This one
	// is written when the record is handed out; the other is written when a
	// cycle ends. A record only grows, so a turn appended after the handing-out
	// is behind neither position and is offered to the next cycle — which is the
	// whole point. Deriving the read position afresh at the end of a cycle would
	// mark that turn judged by a cycle that was never shown it.
	MetaTranscriptOffered = "transcript_offered"
)

// Meta reads a session fact.
func (s *store) Meta(key string) (string, bool, error) {
	db, err := s.conn()
	if err != nil {
		return "", false, err
	}
	var value string
	err = db.QueryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("sessionstate: read meta %q: %w", key, err)
	}
	return value, true, nil
}

// SetMeta records a session fact, replacing any previous value.
//
// Replacing rather than refusing: the baseline moves when a cycle takes a new
// measurement, and that is the ordinary case rather than a conflict. It is safe
// because an unfixed refusal does not depend on the baseline — a failing
// verdict is kept in its own right, so a violation survives a re-measurement.
func (s *store) SetMeta(key, value string) error {
	db, err := s.conn()
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("sessionstate: write meta %q: %w", key, err)
	}
	return nil
}

// SwapMeta records a session fact only while its stored value is still old,
// reporting whether it wrote.
//
// One statement, so the comparison and the write cannot be separated by another
// process's write. The WHERE clause runs against the row under the write lock
// the INSERT already takes; a caller that read a value, decided on it, and calls
// this is told rather than silently overwriting a value its decision never saw.
//
// An absent key matches an empty old, and ONLY an empty old. That makes the
// first write of a key an ordinary case of this rather than a special one, while
// a caller expecting a particular value is still refused when the row is not
// there at all — the two are told apart by the bool, not by having to look
// first.
//
// Both halves are guarded, and each needs its own guard. A bare
// `INSERT ... ON CONFLICT DO UPDATE ... WHERE meta.value = old` applies the
// comparison only on the conflict path, so a caller expecting a value would
// INSERT against an absent row and report success — a swap that matched nothing.
// Guarding the insert alone has the mirror fault: the source row disappears
// whenever old is non-empty, so the conflict never fires and a legitimate update
// is refused. The insert's source row therefore always exists, and it is the
// WHERE on the SELECT that decides the absent case while the WHERE on the
// conflict decides the present one.
func (s *store) SwapMeta(key, old, value string) (bool, error) {
	db, err := s.conn()
	if err != nil {
		return false, err
	}
	res, err := db.Exec(`
		INSERT INTO meta (key, value)
		SELECT ?, ? WHERE ? = '' OR EXISTS (SELECT 1 FROM meta WHERE key = ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value
		WHERE meta.value = ?`, key, value, old, key, old)
	if err != nil {
		return false, fmt.Errorf("sessionstate: swap meta %q: %w", key, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sessionstate: swap meta %q: %w", key, err)
	}
	return n > 0, nil
}
