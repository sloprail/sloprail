package harness

import (
	"encoding/json"
	"os"
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
		[]string{
			"CLAUDE_CONFIG_DIR=" + e.ConfigDir(), "CLAUDE_CODE_SESSION_ID=",
			// What a hook's environment names, so a judge's sr-agent finds its harness —
			// and never the operator's own claude, whatever launched the tests.
			"CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=cli", "CLAUDE_CODE_EXECPATH=",
			// As in a hooked run: the judge's `claude` is the harness's shim, never the operator's.
			"PATH=" + e.shimDir + string(os.PathListSeparator) + e.binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		}, "sr-session", "stop")
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

// shippedFileGuards are the file-guards the sloprail plugin ships for authoring
// guardrails: they judge rule folders and skill reads, which the rule's own files
// (in the range, since the commit that adds a rule is judged) are not about.
var shippedFileGuards = []string{
	"sloprail/file-guard/authoring-slop",
	"sloprail/file-guard/misplaced-declaration",
	"sloprail/file-guard/read-context-doc",
	"sloprail/file-guard/read-file-guard-doc",
	"sloprail/file-guard/read-gate-doc",
	"sloprail/file-guard/read-judge-checks-doc",
	"sloprail/file-guard/read-script-checks-doc",
	"sloprail/file-guard/read-structure-gate-doc",
}

// DisableShippedFileGuards switches off the sloprail plugin's authoring guards in a
// project, so a test about ONE rule is not also a test of those. The commit that
// adds a rule is judged by the rule, which puts the rule's own files in every range
// that starts before it — exactly what the authoring guards exist to judge.
func (e *Env) DisableShippedFileGuards(projDir string) {
	e.t.Helper()
	e.DisablePluginGuardrail(projDir, shippedFileGuards...)
}
