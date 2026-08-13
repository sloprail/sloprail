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
