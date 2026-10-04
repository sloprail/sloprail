package harness

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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

// hookEnv is the environment a hook, or a call made from inside a session, runs with:
// the config dir the session's transcript is under, the harness named the way the mock
// names it (so a judge's sr-agent finds its harness), and a PATH whose `claude` is the
// harness's shim, never the operator's own. sessionID is the session's id, or blank
// where the hook payload, not the environment, names it. One definition, for SessionEnv
// and StopNow alike: they differ in the session id and nothing else.
func (e *Env) hookEnv(sessionID string) []string {
	return []string{
		"CLAUDE_CODE_SESSION_ID=" + sessionID, "CLAUDE_CONFIG_DIR=" + e.ConfigDir(),
		"CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=cli", "CLAUDE_CODE_EXECPATH=",
		"PATH=" + e.shimDir + string(os.PathListSeparator) + e.binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
}

// SessionEnv is the environment for a call made from inside a session the mock
// ran: the session's id and the config dir its transcript is under.
func (e *Env) SessionEnv(sessionID string) []string { return e.hookEnv(sessionID) }

// StopNow runs `sr-session stop` the way the harness does at the end of a turn.
// active is the payload's stop_hook_active: true for the retry after a refusal.
func (e *Env) StopNow(projDir, sessionID string, active bool) Result {
	e.t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"session_id": sessionID, "transcript_path": e.TranscriptPath(projDir, sessionID),
		"cwd": projDir, "stop_hook_active": active, "hook_event_name": "Stop",
	})
	return e.CLIDirectStdinEnv(projDir, string(payload), e.hookEnv(""), "sr-session", "stop")
}

// StopCmd is the command StopNow would run, built and not started, for a test that must
// interrupt a Stop part-way (it owns the process: start it, kill it, wait for it).
func (e *Env) StopCmd(projDir, sessionID string, active bool) *exec.Cmd {
	e.t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"session_id": sessionID, "transcript_path": e.TranscriptPath(projDir, sessionID),
		"cwd": projDir, "stop_hook_active": active, "hook_event_name": "Stop",
	})
	cmd := exec.Command(filepath.Join(e.binDir, "sr-session"), "stop")
	cmd.Dir = projDir
	cmd.Stdin = strings.NewReader(string(payload))
	cmd.Env = append(HostEnv(), "HOME="+e.home, "SLOP_SUBBIN_DIR="+e.binDir)
	cmd.Env = append(cmd.Env, e.autoWatchEnv()...)
	cmd.Env = append(cmd.Env, e.hookEnv("")...)
	return cmd
}

// Blocked reports whether a Stop's output refuses the turn: the blocking form the
// harness honours.
func Blocked(r Result) bool { return strings.Contains(r.Output, `"decision":"block"`) }

// SubagentStopBlocked reports whether the run's stream shows a sub-agent's Stop
// refused with a reason starting with the given text. The mock prints each as
// `SubagentStop blocked (<reason>...`; the refusal reaches the sub-agent, not the
// root's record, so BlockingErrorsFrom cannot see it.
func (r Result) SubagentStopBlocked(reason string) bool {
	return strings.Contains(r.Output, "SubagentStop blocked ("+reason)
}

// SubagentStopBlockedWith reports whether a sub-agent's Stop was refused with a
// reason carrying text on the refusal's first line (the refusal now opens with the
// folder and range, so the text is not at the start): the same message, not any
// text elsewhere in the output.
func (r Result) SubagentStopBlockedWith(text string) bool {
	for _, line := range strings.Split(r.Output, "\n") {
		if i := strings.Index(line, "SubagentStop blocked ("); i >= 0 && strings.Contains(line[i:], text) {
			return true
		}
	}
	return false
}

// AnySubagentStopBlocked reports whether any sub-agent's Stop was refused.
func (r Result) AnySubagentStopBlocked() bool {
	return strings.Contains(r.Output, "SubagentStop blocked (")
}

// shippedGates are the plugin's gates that would refuse a package's own setup: the
// config.yaml a package writes to retire a rule is itself a guarded write.
var shippedGates = []string{"sloprail/gate/grounded-rule-changes"}

// shippedFileGuards are the file-guards the sloprail plugin ships for authoring
// guardrails: they judge rule folders and skill reads, which the rule's own files
// (in the range, since the commit that adds a rule is judged) are not about.
var shippedFileGuards = []string{
	"sloprail/file-guard/authoring-slop",
	"sloprail/file-guard/ci-verify-step",
	"sloprail/file-guard/grounded-rule-changes",
	"sloprail/file-guard/misplaced-declaration",
	"sloprail/file-guard/read-context-doc",
	"sloprail/file-guard/read-file-guard-doc",
	"sloprail/file-guard/read-gate-doc",
	"sloprail/file-guard/read-judge-checks-doc",
	"sloprail/file-guard/read-script-checks-doc",
	"sloprail/file-guard/read-structure-gate-doc",
}
