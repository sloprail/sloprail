package sessionstate

import (
	"errors"
	"fmt"
)

// Ref is one row of a session's recorded refs. See migrations/004_session_refs.sql.
type Ref struct {
	SessionID string
	Folder    string
	// Name is the full ref name, or "detached/<sha12>" for a detached-HEAD commit.
	Name     string
	FirstTip string
	Tip      string
	AgentID  string
}

// RecordRef writes a ref's tip: the first sighting sets FirstTip, a later one only
// moves Tip.
func (s *store) RecordRef(r Ref) error {
	if r.SessionID == "" || r.Folder == "" || r.Name == "" || r.Tip == "" {
		return errors.New("sessionstate: a ref needs a session, a folder, a name and a tip")
	}
	db, err := s.conn()
	if err != nil {
		return err
	}
	first := r.FirstTip
	if first == "" {
		first = r.Tip
	}
	if _, err := db.Exec(`
		INSERT INTO session_refs (session_id, folder, ref, first_tip, tip, agent_id)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (session_id, folder, ref) DO UPDATE SET tip = excluded.tip`,
		r.SessionID, r.Folder, r.Name, first, r.Tip, r.AgentID); err != nil {
		return fmt.Errorf("sessionstate: record ref %q: %w", r.Name, err)
	}
	return nil
}

// Refs lists a session's recorded refs in one folder (every folder when folder is "").
func (s *store) Refs(sessionID, folder string) ([]Ref, error) {
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	q := `SELECT session_id, folder, ref, first_tip, tip, agent_id FROM session_refs WHERE session_id = ?`
	args := []any{sessionID}
	if folder != "" {
		q += ` AND folder = ?`
		args = append(args, folder)
	}
	rows, err := db.Query(q+` ORDER BY folder, ref`, args...)
	if err != nil {
		return nil, fmt.Errorf("sessionstate: list refs: %w", err)
	}
	defer rows.Close()
	var out []Ref
	for rows.Next() {
		var r Ref
		if err := rows.Scan(&r.SessionID, &r.Folder, &r.Name, &r.FirstTip, &r.Tip, &r.AgentID); err != nil {
			return nil, fmt.Errorf("sessionstate: list refs: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
