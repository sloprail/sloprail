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
	// MetaSessionStart is the HEAD the session FIRST began at, written once and never
	// moved. MetaBaselineCommit is re-taken when the tree leaves its history (an amend,
	// a rebase, a branch switch), which is right for the difference a context measures
	// but wrong for a file-guard's range: re-taking it at the first Stop after an amend
	// would put the start after the commits made in the session, and they would never
	// be judged. A file-guard's range reads this one; when it is no longer reachable
	// the range anchors on the remote instead (gitrepo.ResolveRange).
	MetaSessionStart = "session_start_commit"
	// SessionStartUnborn is MetaSessionStart's value for a session that began in a
	// repository with no commit yet: its range starts at git's empty tree, so the commits
	// the agent makes in its first turn are judged.
	SessionStartUnborn = "unborn"
	// MetaBaselineBranch is which line of history that point belongs to. Kept
	// beside the commit because an agent may switch branches mid-session, and a
	// point recorded on the line it left describes a history the tree no longer
	// has.
	MetaBaselineBranch = "baseline_branch"
	// MetaBaselineAtStop marks a baseline that was first recorded at a sub-agent's
	// OWN Stop, not as its work began. It is the HEAD the sub-agent's commits had
	// already produced, so a file-guard's range must not be measured from it.
	MetaBaselineAtStop = "baseline_at_stop"
	// MetaTranscriptRead is how far the session's own record has been read. A
	// position rather than a state, because a record only grows.
	MetaTranscriptRead = "transcript_read"

	// MetaRefsAtStart is every branch tip (local and remote-tracking) the root folder had
	// when the session began, as a JSON object of ref name to commit. What a branch
	// already held then is not the session's work: a tip inside that history is never
	// judged as one the session committed on.
	MetaRefsAtStart = "refs_at_start"

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

	// MetaStopRefusals is how many times in a row a Stop has been refused in the
	// current sequence — the first Stop of a turn and every retry the harness
	// sends after a refusal. Counted so the project's stop_hook_block_cap can end
	// a refusal loop after a set number of blocks; reset when a Stop completes or
	// a new sequence begins.
	MetaStopRefusals = "stop_refusals"

	// MetaCommitRequired is the commit-required gate's loop breaker: how many
	// times in a row it refused the same set of uncommitted paths, as
	// "<hash of the set>:<count>". Its own counter rather than MetaStopRefusals,
	// because that one counts every refusal of a Stop while this one counts one
	// reason for refusing, and it is keyed on the set so committing some of the
	// work starts the count again.
	MetaCommitRequired = "commit_required"

	// MetaStopSeenRecord is how far the previous judged Stop read the record for
	// tags, as "<entry count>:<uuid of the last entry>". Text up to there was
	// already shown to a Stop; while the cycle is still open it is delivered
	// again, and its tags are marked `seen`. A count because a uuid can repeat.
	MetaStopSeenRecord = "stop_seen_record"

	// MetaStopSeenFiles is the content fingerprint of every file the previous
	// judged Stop was handed a Post file event for, as a JSON object of path to
	// fingerprint. A file whose fingerprint is unchanged at the next Stop is
	// being re-sent (the difference is taken against the session's baseline, so
	// it is delivered on every Stop until committed) and is marked `seen`.
	MetaStopSeenFiles = "stop_seen_files"

	// MetaCitedPending is the cited changes permitted pre-tool calls are about
	// to make, not yet known to have landed: a JSON list of {path, abs, change}.
	// The next hook of the same session settles each — kept in
	// MetaCitedChanges when the file now holds what the change produces,
	// dropped when it does not (the call failed, was denied, or never ran).
	MetaCitedPending = "cited_pending"

	// MetaCitations is, per file path, its history this session: every cited
	// change that LANDED (the citations it rode on, and the file's state before
	// and after it, by content hash) and every change the agent did not make,
	// as a JSON object of path to a list of points. Contents are stored once
	// each, under their own keys. The Post
	// events at Stop come from the tree difference, which knows nothing of the
	// commands that made it; this is how they carry the citations the change
	// was made with — and how the parts of the change no citation rode on are
	// told apart from the parts one did.
	MetaCitations = "citations"

	// MetaCitedCycle is where the session's current cycle stands for cited
	// changes — before its first hook, open, or ended, and how the agent left
	// each changed file at its last Stop — as JSON. It is how the first hook
	// of a cycle tells a change the agent never made (the user's edit between
	// turns, a branch switch, a file dirty when the session began) from one it
	// made.
	MetaCitedCycle = "cited_cycle"
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

// MetaKeys lists the meta keys that start with prefix, in key order.
func (s *store) MetaKeys(prefix string) ([]string, error) {
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT key FROM meta WHERE substr(key, 1, ?) = ? ORDER BY key`, len(prefix), prefix)
	if err != nil {
		return nil, fmt.Errorf("sessionstate: list meta %q: %w", prefix, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("sessionstate: list meta %q: %w", prefix, err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// DeleteMeta forgets a session fact. Deleting a key never written is not an
// error.
func (s *store) DeleteMeta(key string) error {
	db, err := s.conn()
	if err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM meta WHERE key = ?`, key); err != nil {
		return fmt.Errorf("sessionstate: delete meta %q: %w", key, err)
	}
	return nil
}
