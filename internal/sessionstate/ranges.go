package sessionstate

import (
	"errors"
	"fmt"
)

// Who added a tracked range.
const (
	// RangeAuto: the engine tracked it when it discovered the folder (or the branch the
	// agent moved to).
	RangeAuto = "auto"
	// RangeAgent: the agent stated it (`sr-session refs track`).
	RangeAgent = "agent"
)

// TrackedRange is one range of commits a session answers for, in one folder: from Base to
// Head. See migrations/007_session_ranges.sql.
type TrackedRange struct {
	SessionID string
	// Folder is the folder's git root, as registered.
	Folder string
	// Head is a branch name or a commit sha; Base a revision. HeadSHA is the commit Head last
	// pointed at, kept so a branch that has gone can still be verified at what it was.
	Head    string
	Base    string
	HeadSHA string
	// AddedBy is RangeAuto or RangeAgent.
	AddedBy string
	// UntrackedReason is "" while tracked; the reason the agent gave for dropping it.
	UntrackedReason string
	// AgentID names the sub-agent the range belongs to; empty for the root.
	AgentID string
}

// Tracked reports whether the range is still one the session answers for.
func (r TrackedRange) Tracked() bool { return r.UntrackedReason == "" }

// TrackRange records a range, replacing the base of one already tracked for the same
// (folder, head) — the agent moving a base — and tracking it again if it was untracked. An
// automatic tracking never overrides what the agent stated or dropped: it only adds a range
// that is not there yet, and refreshes the commit its head last pointed at.
func (s *store) TrackRange(r TrackedRange) error {
	if r.SessionID == "" || r.Folder == "" || r.Head == "" {
		return errors.New("sessionstate: a tracked range needs a session, a folder and a head")
	}
	if r.AddedBy == "" {
		r.AddedBy = RangeAuto
	}
	db, err := s.conn()
	if err != nil {
		return err
	}
	if r.AddedBy == RangeAuto {
		_, err = db.Exec(`
			INSERT INTO session_ranges (session_id, folder, head, base, head_sha, added_by, untracked_reason, agent_id)
			VALUES (?, ?, ?, ?, ?, ?, '', ?)
			ON CONFLICT (session_id, folder, head) DO UPDATE SET head_sha = excluded.head_sha`,
			r.SessionID, r.Folder, r.Head, r.Base, r.HeadSHA, r.AddedBy, r.AgentID)
	} else {
		_, err = db.Exec(`
			INSERT INTO session_ranges (session_id, folder, head, base, head_sha, added_by, untracked_reason, agent_id)
			VALUES (?, ?, ?, ?, ?, ?, '', ?)
			ON CONFLICT (session_id, folder, head) DO UPDATE SET
				base = excluded.base, head_sha = excluded.head_sha, added_by = excluded.added_by, untracked_reason = ''`,
			r.SessionID, r.Folder, r.Head, r.Base, r.HeadSHA, r.AddedBy, r.AgentID)
	}
	if err != nil {
		return fmt.Errorf("sessionstate: track range %q in %q: %w", r.Head, r.Folder, err)
	}
	return nil
}

// UntrackRange stops answering for a range, saying why. An untracked range stays listed,
// reason and all, so the Stop can name it; a range that was never tracked is recorded as
// untracked so an automatic tracking does not bring it back.
func (s *store) UntrackRange(sessionID, folder, head, reason, agentID string) error {
	if reason == "" {
		return errors.New("sessionstate: untracking a range needs a reason")
	}
	db, err := s.conn()
	if err != nil {
		return err
	}
	if _, err := db.Exec(`
		INSERT INTO session_ranges (session_id, folder, head, base, added_by, untracked_reason, agent_id)
		VALUES (?, ?, ?, '', ?, ?, ?)
		ON CONFLICT (session_id, folder, head) DO UPDATE SET untracked_reason = excluded.untracked_reason`,
		sessionID, folder, head, RangeAgent, reason, agentID); err != nil {
		return fmt.Errorf("sessionstate: untrack range %q in %q: %w", head, folder, err)
	}
	return nil
}

// Ranges lists a session's ranges, tracked and untracked, by folder and head.
func (s *store) Ranges(sessionID string) ([]TrackedRange, error) {
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`
		SELECT session_id, folder, head, base, head_sha, added_by, untracked_reason, agent_id
		FROM session_ranges WHERE session_id = ? ORDER BY folder, head`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("sessionstate: list ranges: %w", err)
	}
	defer rows.Close()
	var out []TrackedRange
	for rows.Next() {
		var r TrackedRange
		if err := rows.Scan(&r.SessionID, &r.Folder, &r.Head, &r.Base, &r.HeadSHA, &r.AddedBy, &r.UntrackedReason, &r.AgentID); err != nil {
			return nil, fmt.Errorf("sessionstate: list ranges: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
