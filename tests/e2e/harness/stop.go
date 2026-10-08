package harness

import (
	"encoding/json"
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
	return e.driver.HookEnv(e, sessionID)
}

// SessionEnv is the environment for a call made from inside a session the mock
// ran: the session's id and the config dir its transcript is under.
func (e *Env) SessionEnv(sessionID string) []string { return e.hookEnv(sessionID) }

// IdentityPayload is a hook payload naming only the session and its project folder.
func (e *Env) IdentityPayload(projDir, sessionID string) string {
	return e.driver.IdentityPayload(e, projDir, sessionID)
}

// StopNow runs `sr-session stop` the way the harness does at the end of a turn.
// active is the payload's stop_hook_active: true for the retry after a refusal.
func (e *Env) StopNow(projDir, sessionID string, active bool) Result {
	e.t.Helper()
	payload := e.driver.StopPayload(e, projDir, sessionID, active)
	return e.CLIDirectStdinEnv(projDir, payload, e.hookEnv(""), "sr-session", "stop")
}

// StopFrom is StopNow for a hook that reports another folder as its own (the agent moved to
// another worktree) while its record still names the project the session began in: the
// payload is the session's own Stop payload with the folder it names swapped for dir, and
// the hook runs there.
func (e *Env) StopFrom(projDir, sessionID, dir string) Result {
	e.t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(e.driver.StopPayload(e, projDir, sessionID, false)), &payload); err != nil {
		e.t.Fatalf("harness: stop payload: %v", err)
	}
	if _, ok := payload["cwd"]; ok {
		payload["cwd"] = dir
	}
	if _, ok := payload["workspace_roots"]; ok {
		payload["workspace_roots"] = []string{resolveWorkDir(dir)}
	}
	body, _ := json.Marshal(payload)
	return e.CLIDirectStdinEnv(dir, string(body), e.hookEnv(""), "sr-session", "stop")
}

// StopCmd is the command StopNow would run, built and not started, for a test that must
// interrupt a Stop part-way (it owns the process: start it, kill it, wait for it).
func (e *Env) StopCmd(projDir, sessionID string, active bool) *exec.Cmd {
	e.t.Helper()
	payload := e.driver.StopPayload(e, projDir, sessionID, active)
	cmd := exec.Command(filepath.Join(e.binDir, "sr-session"), "stop")
	cmd.Dir = projDir
	cmd.Stdin = strings.NewReader(payload)
	cmd.Env = append(HostEnv(), "HOME="+e.home, "SLOP_SUBBIN_DIR="+e.binDir)
	cmd.Env = append(cmd.Env, e.autoWatchEnv()...)
	cmd.Env = append(cmd.Env, e.hookEnv("")...)
	return cmd
}

// Blocked reports whether a Stop's output refuses the turn: the blocking form the
// harness honours.
func Blocked(r Result) bool { return mustDriver().StopBlocked(r.Output) }

// SubagentStopBlocked reports whether a sub-agent of the session was refused at its Stop with a
// reason containing text ("" for any refusal). It reads the sub-agents' own transcripts, where
// Claude Code records a SubagentStop refusal (the "Stop hook feedback" turn and its
// hook_blocking_error), through SubagentBlockingErrors: a refusal counts only if the sub-agent
// was told it. The run's stream carries no such line.
func (e *Env) SubagentStopBlocked(projDir, sessionID, text string) bool {
	e.t.Helper()
	for _, b := range e.SubagentBlockingErrors(projDir, sessionID) {
		if strings.Contains(b, text) {
			return true
		}
	}
	return false
}

// SubagentStopFeedbackCount is how many times the session's sub-agents were sent round again after a
// refused Stop: the "Stop hook feedback" turns in their own transcripts, counted without the
// de-duplication SubagentBlockingErrors does (a refusal repeated with the same words counts again).
func (e *Env) SubagentStopFeedbackCount(projDir, sessionID string) int {
	e.t.Helper()
	e.requireSubagentStopObservable("SubagentStopFeedbackCount")
	return e.driver.SubagentFeedbackCount(e.subagentRecords(projDir, sessionID))
}

// NoSubagentStopBlock reports that no sub-agent of the session was refused at its Stop: the strict
// negative (AnySubagentBlockingErrors), which a refusal recorded without its feedback cannot satisfy.
//
// Where the harness never fires a sub-agent's stop hook (no CapSubagentLifecycleHooks: Cursor) there
// is no refusal to read, and "none recorded" would be true of any run. The negative is then the
// cause itself: no sub-agent ever reached the session's registry (which the start and stop hooks
// feed), so no sub-agent Stop was judged, let alone refused.
func (e *Env) NoSubagentStopBlock(projDir, sessionID string) bool {
	e.t.Helper()
	if !HasCap(e.t, CapSubagentLifecycleHooks) {
		return e.subagentRegistryRows(projDir, sessionID) == 0
	}
	return len(e.AnySubagentBlockingErrors(projDir, sessionID)) == 0
}

// subagentRegistryRows is how many sub-agents the session's registry (`sr-session agents list`) holds.
func (e *Env) subagentRegistryRows(projDir, sessionID string) int {
	e.t.Helper()
	r := e.CLIDirectEnv(projDir, e.SessionEnv(sessionID), "sr-session", "agents", "list", "--json")
	if r.Code != 0 {
		e.t.Fatalf("harness: agents list: exit %d:\n%s", r.Code, r.Output)
	}
	var rows []json.RawMessage
	if err := json.Unmarshal([]byte(r.Output), &rows); err != nil {
		e.t.Fatalf("harness: agents list --json is not JSON (%v):\n%s", err, r.Output)
	}
	return len(rows)
}

// requireSubagentStopObservable fails the test that asks for a sub-agent's Stop refusals on a harness
// that never fires the hook: the answer would be empty whatever happened, so a caller must branch on
// CapSubagentLifecycleHooks and assert something real on the other side.
func (e *Env) requireSubagentStopObservable(what string) {
	e.t.Helper()
	if !HasCap(e.t, CapSubagentLifecycleHooks) {
		e.t.Fatalf("harness: %s asked on %s, which fires no sub-agent stop hook (no %s): branch on the capability", what, Selected(e.t), CapSubagentLifecycleHooks)
	}
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
	"sloprail/file-guard/rule-tests-pass",
	"sloprail/file-guard/rule-tests-rigorous",
}
