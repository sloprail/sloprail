package sessionstate

import (
	"errors"
	"fmt"
	"time"
)

// The status of a sub-agent in the registry (see migrations/007_session_agents.sql).
const (
	AgentRunning   = "running"
	AgentCompleted = "completed"
	AgentFailed    = "failed"
	AgentKilled    = "killed"
	AgentStopped   = "stopped"
	// AgentStale is the one status sloprail sets by itself: a running agent that stayed silent
	// past the stale threshold, or whose harness process is gone. Its ranges are judged.
	AgentStale = "stale"
)

// Agent is one sub-agent of a session as the registry knows it.
type Agent struct {
	SessionID string
	AgentID   string
	Status    string
	AgentType string
	// Background: the dispatching record showed the agent launched in the background. Only
	// such an agent's ranges are ever left unjudged.
	Background bool
	StartedAt  time.Time
	// EndedAt is zero while the agent runs.
	EndedAt time.Time
	// LastSeenAt is the last signal the agent itself gave (a hook of its own); zero when none.
	LastSeenAt     time.Time
	TranscriptPath string
	// OwnerPID and OwnerProcStart name the harness process the agent was seen under; 0 / "" unknown.
	OwnerPID       int
	OwnerProcStart string
	// Ranges are the tracked ranges the agent owns (session_refs rows with its agent_id).
	Ranges []TrackedRange
}

// Running reports whether the registry holds the agent as still running.
func (a Agent) Running() bool { return a.Status == AgentRunning }

// TerminalAgentStatus reports whether the status is a verdict of the harness: after it the agent does not run.
func TerminalAgentStatus(s string) bool {
	switch s {
	case AgentCompleted, AgentFailed, AgentKilled, AgentStopped:
		return true
	}
	return false
}

// AgentSignal is one fact about an agent. Zero fields are "not said".
type AgentSignal struct {
	SessionID, AgentID string
	AgentType          string
	TranscriptPath     string
	OwnerPID           int
	OwnerProcStart     string
	At                 time.Time
}

func (s AgentSignal) check() error {
	if s.SessionID == "" || s.AgentID == "" {
		return errors.New("sessionstate: an agent signal needs a session and an agent id")
	}
	return nil
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// StartAgent records that an agent began (its SubagentStart): a new agent is running, and one
// seen before is running again (a finished agent the dispatcher resumed).
func (s *store) StartAgent(sig AgentSignal) error {
	if err := sig.check(); err != nil {
		return err
	}
	db, err := s.conn()
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT INTO session_agents (session_id, agent_id, status, agent_type, started_at, last_seen_at, transcript_path, owner_pid, owner_proc_start)
		VALUES (?, ?, 'running', ?, ?, ?, ?, ?, ?)
		ON CONFLICT (session_id, agent_id) DO UPDATE SET status = 'running', ended_at = 0,
			agent_type = CASE WHEN excluded.agent_type <> '' THEN excluded.agent_type ELSE session_agents.agent_type END,
			started_at = CASE WHEN session_agents.started_at = 0 THEN excluded.started_at ELSE session_agents.started_at END,
			last_seen_at = excluded.last_seen_at,
			transcript_path = CASE WHEN excluded.transcript_path <> '' THEN excluded.transcript_path ELSE session_agents.transcript_path END,
			owner_pid = CASE WHEN excluded.owner_pid <> 0 THEN excluded.owner_pid ELSE session_agents.owner_pid END,
			owner_proc_start = CASE WHEN excluded.owner_pid <> 0 THEN excluded.owner_proc_start ELSE session_agents.owner_proc_start END`,
		sig.SessionID, sig.AgentID, sig.AgentType, unix(sig.At), unix(sig.At), sig.TranscriptPath, sig.OwnerPID, sig.OwnerProcStart)
	if err != nil {
		return fmt.Errorf("sessionstate: start agent %q: %w", sig.AgentID, err)
	}
	return nil
}

// TouchAgent records a hook of the agent's own (its tool call): the agent is alive. An agent
// not known yet is recorded as running; one marked completed or stale is running again (it is
// calling tools). A failed, killed or stopped agent stays so: that is the harness's verdict.
func (s *store) TouchAgent(sig AgentSignal) error {
	if err := sig.check(); err != nil {
		return err
	}
	db, err := s.conn()
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT INTO session_agents (session_id, agent_id, status, agent_type, started_at, last_seen_at, transcript_path, owner_pid, owner_proc_start)
		VALUES (?, ?, 'running', ?, ?, ?, ?, ?, ?)
		ON CONFLICT (session_id, agent_id) DO UPDATE SET
			status = CASE WHEN session_agents.status IN ('completed', 'stale') THEN 'running' ELSE session_agents.status END,
			ended_at = CASE WHEN session_agents.status IN ('completed', 'stale') THEN 0 ELSE session_agents.ended_at END,
			last_seen_at = excluded.last_seen_at,
			transcript_path = CASE WHEN excluded.transcript_path <> '' THEN excluded.transcript_path ELSE session_agents.transcript_path END,
			owner_pid = CASE WHEN session_agents.owner_pid = 0 THEN excluded.owner_pid ELSE session_agents.owner_pid END,
			owner_proc_start = CASE WHEN session_agents.owner_pid = 0 THEN excluded.owner_proc_start ELSE session_agents.owner_proc_start END`,
		sig.SessionID, sig.AgentID, sig.AgentType, unix(sig.At), unix(sig.At), sig.TranscriptPath, sig.OwnerPID, sig.OwnerProcStart)
	if err != nil {
		return fmt.Errorf("sessionstate: touch agent %q: %w", sig.AgentID, err)
	}
	return nil
}

// NoteAgentLaunch records what the dispatching record shows about a background launch: an agent
// not known yet is added as running and background; a known one only gains the background mark,
// its status untouched (a record read again must not bring a finished agent back).
func (s *store) NoteAgentLaunch(sig AgentSignal) error {
	if err := sig.check(); err != nil {
		return err
	}
	db, err := s.conn()
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT INTO session_agents (session_id, agent_id, status, background, started_at, owner_pid, owner_proc_start)
		VALUES (?, ?, 'running', 1, ?, ?, ?)
		ON CONFLICT (session_id, agent_id) DO UPDATE SET background = 1`,
		sig.SessionID, sig.AgentID, unix(sig.At), sig.OwnerPID, sig.OwnerProcStart)
	if err != nil {
		return fmt.Errorf("sessionstate: note launch of agent %q: %w", sig.AgentID, err)
	}
	return nil
}

// EndAgent records the end of an agent's run with a terminal status. The first terminal verdict
// stands (a SubagentStop's "completed" is not rewritten by a later notification); a running or
// stale agent takes it. An agent not known yet is added, ended.
func (s *store) EndAgent(sessionID, agentID, status string, at time.Time) error {
	if !TerminalAgentStatus(status) {
		return fmt.Errorf("sessionstate: %q is not a terminal agent status", status)
	}
	sig := AgentSignal{SessionID: sessionID, AgentID: agentID}
	if err := sig.check(); err != nil {
		return err
	}
	db, err := s.conn()
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT INTO session_agents (session_id, agent_id, status, started_at, ended_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (session_id, agent_id) DO UPDATE SET status = excluded.status, ended_at = excluded.ended_at
		WHERE session_agents.status IN ('running', 'stale')`,
		sessionID, agentID, status, unix(at), unix(at))
	if err != nil {
		return fmt.Errorf("sessionstate: end agent %q: %w", agentID, err)
	}
	return nil
}

// MarkAgentStale sets a running agent stale: it is no longer waited for. Any other status stays.
func (s *store) MarkAgentStale(sessionID, agentID string, at time.Time) error {
	db, err := s.conn()
	if err != nil {
		return err
	}
	if _, err := db.Exec(`UPDATE session_agents SET status = 'stale', ended_at = ? WHERE session_id = ? AND agent_id = ? AND status = 'running'`,
		unix(at), sessionID, agentID); err != nil {
		return fmt.Errorf("sessionstate: mark agent %q stale: %w", agentID, err)
	}
	return nil
}

// NewAgentSignal reports whether a signal of a session is new, recording it: the signal is the
// record that carried a task-notification, so one is applied once.
func (s *store) NewAgentSignal(sessionID, signal string) (bool, error) {
	db, err := s.conn()
	if err != nil {
		return false, err
	}
	res, err := db.Exec(`INSERT OR IGNORE INTO session_agent_signals (session_id, signal) VALUES (?, ?)`, sessionID, signal)
	if err != nil {
		return false, fmt.Errorf("sessionstate: record agent signal: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Agents lists a session's sub-agents, oldest first, each with the tracked ranges it owns.
func (s *store) Agents(sessionID string) ([]Agent, error) {
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`
		SELECT agent_id, status, agent_type, background, started_at, ended_at, last_seen_at, transcript_path, owner_pid, owner_proc_start
		FROM session_agents WHERE session_id = ? ORDER BY started_at, agent_id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("sessionstate: list agents: %w", err)
	}
	var out []Agent
	for rows.Next() {
		a := Agent{SessionID: sessionID}
		var bg int
		var started, ended, seen int64
		if err := rows.Scan(&a.AgentID, &a.Status, &a.AgentType, &bg, &started, &ended, &seen, &a.TranscriptPath, &a.OwnerPID, &a.OwnerProcStart); err != nil {
			rows.Close()
			return nil, fmt.Errorf("sessionstate: read agent: %w", err)
		}
		a.Background = bg != 0
		a.StartedAt, a.EndedAt, a.LastSeenAt = fromUnix(started), fromUnix(ended), fromUnix(seen)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("sessionstate: list agents: %w", err)
	}
	rows.Close()
	ranges, err := s.Ranges(sessionID)
	if err != nil {
		return nil, err
	}
	for i := range out {
		for _, r := range ranges {
			if r.AgentID == out[i].AgentID && r.Tracked() {
				out[i].Ranges = append(out[i].Ranges, r)
			}
		}
	}
	return out, nil
}

func fromUnix(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}
