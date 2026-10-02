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
// Head. It is a row of session_refs (see migrations/006_session_refs_tracked_ranges.sql):
// Head is the row's ref, HeadSHA its tip.
type TrackedRange struct {
	SessionID string
	// Folder is the folder's git root, as registered.
	Folder string
	// Head is a branch name (the range follows it) or a commit sha; Base a revision, "" for a
	// row an older engine recorded (the reader computes it). HeadSHA is the commit Head last
	// pointed at, kept so a branch that has gone can still be verified at what it was.
	Head    string
	Base    string
	HeadSHA string
	// AddedBy is RangeAuto or RangeAgent.
	AddedBy string
	// UntrackedReason is "" while tracked; the reason the agent gave for dropping it.
	UntrackedReason string
	// AbandonedTip is the tip the range was untracked at; "" while tracked. An untracked range
	// whose branch tip moves past it is tracked again by the next automatic tracking.
	AbandonedTip string
	// AgentID names the sub-agent the range belongs to; empty for the root.
	AgentID string
}

// Tracked reports whether the range is still one the session answers for.
func (r TrackedRange) Tracked() bool { return r.UntrackedReason == "" }

// TrackRange records a range. An automatic tracking only adds what is not there and refreshes
// the commit its head last pointed at: what the agent stated or dropped stays so. The agent's
// replaces the base and tracks the range again if it was untracked.
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
			INSERT INTO session_refs (session_id, folder, ref, first_tip, tip, agent_id, base, added_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (session_id, folder, ref) DO UPDATE SET tip = excluded.tip,
				base = CASE WHEN session_refs.base = '' THEN excluded.base ELSE session_refs.base END,
				untracked_reason = CASE WHEN session_refs.untracked_reason <> '' AND session_refs.abandoned_tip <> '' AND session_refs.abandoned_tip <> excluded.tip THEN '' ELSE session_refs.untracked_reason END,
				abandoned_tip = CASE WHEN session_refs.untracked_reason <> '' AND session_refs.abandoned_tip <> '' AND session_refs.abandoned_tip <> excluded.tip THEN '' ELSE session_refs.abandoned_tip END`,
			r.SessionID, r.Folder, r.Head, r.HeadSHA, r.HeadSHA, r.AgentID, r.Base, r.AddedBy)
	} else {
		_, err = db.Exec(`
			INSERT INTO session_refs (session_id, folder, ref, first_tip, tip, agent_id, base, added_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (session_id, folder, ref) DO UPDATE SET
				base = excluded.base, tip = excluded.tip, added_by = excluded.added_by, untracked_reason = '', abandoned_tip = ''`,
			r.SessionID, r.Folder, r.Head, r.HeadSHA, r.HeadSHA, r.AgentID, r.Base, r.AddedBy)
	}
	if err != nil {
		return fmt.Errorf("sessionstate: track range %q in %q: %w", r.Head, r.Folder, err)
	}
	return nil
}

// UntrackRange stops answering for a range, saying why. An untracked range stays listed,
// reason and all, so the Stop can name it; a range that was never tracked is recorded as
// untracked so an automatic tracking does not bring it back while its branch stays at tip (the
// tip it was untracked at; "" never brings it back). A branch whose tip moves is tracked again.
func (s *store) UntrackRange(sessionID, folder, head, reason, agentID, tip string) error {
	if reason == "" {
		return errors.New("sessionstate: untracking a range needs a reason")
	}
	db, err := s.conn()
	if err != nil {
		return err
	}
	if _, err := db.Exec(`
		INSERT INTO session_refs (session_id, folder, ref, first_tip, tip, agent_id, base, added_by, untracked_reason, abandoned_tip)
		VALUES (?, ?, ?, ?, ?, ?, '', ?, ?, ?)
		ON CONFLICT (session_id, folder, ref) DO UPDATE SET untracked_reason = excluded.untracked_reason,
			abandoned_tip = excluded.abandoned_tip, tip = CASE WHEN excluded.tip = '' THEN session_refs.tip ELSE excluded.tip END`,
		sessionID, folder, head, tip, tip, agentID, RangeAgent, reason, tip); err != nil {
		return fmt.Errorf("sessionstate: untrack range %q in %q: %w", head, folder, err)
	}
	return nil
}

// SetRangeBase fills the base of a range an older engine recorded without one.
func (s *store) SetRangeBase(sessionID, folder, head, base string) error {
	db, err := s.conn()
	if err != nil {
		return err
	}
	if _, err := db.Exec(`UPDATE session_refs SET base = ? WHERE session_id = ? AND folder = ? AND ref = ? AND base = ''`,
		base, sessionID, folder, head); err != nil {
		return fmt.Errorf("sessionstate: set base of %q in %q: %w", head, folder, err)
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
		SELECT session_id, folder, ref, base, tip, added_by, untracked_reason, agent_id, abandoned_tip
		FROM session_refs WHERE session_id = ? ORDER BY folder, ref`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("sessionstate: list ranges: %w", err)
	}
	defer rows.Close()
	var out []TrackedRange
	for rows.Next() {
		var r TrackedRange
		if err := rows.Scan(&r.SessionID, &r.Folder, &r.Head, &r.Base, &r.HeadSHA, &r.AddedBy, &r.UntrackedReason, &r.AgentID, &r.AbandonedTip); err != nil {
			return nil, fmt.Errorf("sessionstate: list ranges: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
