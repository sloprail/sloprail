package harness

import (
	"encoding/json"
	"strings"
)

// Driving a Stop exactly, and reading what the session's file-guards concluded.
//
// Most tests let the mock run a scenario and end it with a Stop; a test that
// needs a Stop at a precise moment — after the tree was arranged just so, or
// repeatedly against the same state — sends the hook payload itself, the way the
// harness does.

// NoSessionEnv is the environment for a call made outside any session. Blank, not
// absent: an ambient CLAUDE_CODE_SESSION_ID would resolve the developer's own.
var NoSessionEnv = []string{"CLAUDE_CODE_SESSION_ID=", "CLAUDECODE="}

// SessionEnv is the environment for a call made from inside a session the mock
// ran: the session's id and the config dir its transcript is under.
func (e *Env) SessionEnv(sessionID string) []string {
	return []string{"CLAUDE_CODE_SESSION_ID=" + sessionID, "CLAUDE_CONFIG_DIR=" + e.ConfigDir(), "CLAUDECODE="}
}

// StopNow runs `sr-session stop` the way the harness does at the end of a turn.
// active is the payload's stop_hook_active: true for the retry after a refusal.
func (e *Env) StopNow(projDir, sessionID string, active bool) Result {
	e.t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"session_id": sessionID, "transcript_path": e.TranscriptPath(projDir, sessionID),
		"cwd": projDir, "stop_hook_active": active, "hook_event_name": "Stop",
	})
	return e.CLIDirectStdinEnv(projDir, string(payload),
		[]string{"CLAUDE_CONFIG_DIR=" + e.ConfigDir(), "CLAUDECODE=", "CLAUDE_CODE_SESSION_ID="}, "sr-session", "stop")
}

// Blocked reports whether a Stop's output refuses the turn: the blocking form the
// harness honours.
func Blocked(r Result) bool { return strings.Contains(r.Output, `"decision":"block"`) }

// ChecksStatus is `sr-checks status` for a session, with the given flags.
func (e *Env) ChecksStatus(projDir, sessionID string, args ...string) string {
	e.t.Helper()
	return e.CLIDirectEnv(projDir, e.SessionEnv(sessionID), "sr-checks", append([]string{"status"}, args...)...).Output
}

// ChecksSQL is `sr-checks sql` for a session.
func (e *Env) ChecksSQL(projDir, sessionID, query string) Result {
	e.t.Helper()
	return e.CLIDirectEnv(projDir, e.SessionEnv(sessionID), "sr-checks", "sql", query)
}

// SubagentStopBlocked reports whether the run's stream shows a sub-agent's Stop
// refused with a reason starting with the given text. The mock prints each as
// `SubagentStop blocked (<reason>...`; the refusal reaches the sub-agent, not the
// root's record, so BlockingErrorsFrom cannot see it.
func (r Result) SubagentStopBlocked(reason string) bool {
	return strings.Contains(r.Output, "SubagentStop blocked ("+reason)
}

// AnySubagentStopBlocked reports whether any sub-agent's Stop was refused.
func (r Result) AnySubagentStopBlocked() bool {
	return strings.Contains(r.Output, "SubagentStop blocked (")
}
