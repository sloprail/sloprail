package harness

import (
	"encoding/json"
	"fmt"
	"github.com/sloprail/sloprail/internal/harness"
	cursorharness "github.com/sloprail/sloprail/internal/harness/cursor"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// cursorDriver is the Driver for Cursor's agent, run through a10n-cursor-mock (the
// version pinned in CURSOR_MOCK_VERSION, installed by `make mock`): a scripted
// `cursor-agent -p` whose scenario script prints one assistant step per line (the
// stream-json the other mocks read) and which fires the hooks of the plugins named
// with --plugin-dir and of <workspace>/.cursor/hooks.json.
//
//   - The tools are Shell (Bash), Write, StrReplace (Edit), Read, Grep, Delete and
//     Task (a sub-agent, with no worktree option). A step with no such tool (
//     background commands, raw results) is unsupported.
//   - The transcript has no tool-call ids, so a step cannot mark itself done by its
//     id as on the other harnesses: the script counts the records the transcript holds
//     (a tool call, a prompt, which a compaction writes again) and plays the step
//     that count names.
//   - Sessions are named by the mock (the id its first frame prints), so a test's
//     session id is an alias of it (Env.harnessIDs), as for Codex.
//   - In `-p` the real cursor-agent never fires the stop hook (only its TUI does). Every
//     run sets A10N_CURSOR_MOCK_STOP=1, the mock's declared opt-in (a deviation backed by
//     the TUI recordings): it fires afterAgentResponse and stop each turn, and a stop
//     hook's followup_message becomes the next turn. Runs of a launched agent (a judge,
//     AgentShim) do not set it.
//   - The sloprail plugin is loaded with --plugin-dir; extra plugins are the user's
//     local plugins, <home>/.cursor/plugins/local/<name>.
type cursorDriver struct{}

func (cursorDriver) Name() string { return "cursor" }

// Caps: no skill tool, no ask-user-question, no worktree isolation, no receipt naming a
// background task (spec/capabilities, providers.cursor of harness-mocks). The stop hook is
// there because every run opts into the mock's Stop (A10N_CURSOR_MOCK_STOP=1): a scenario
// then ends with the agent's own `sr-checks run`, which the Stop verifies.
func (cursorDriver) Caps() []string {
	return []string{CapSubagents, CapPlugins, CapStopHooks, CapForkResumeCompact, CapTranscript}
}

func (cursorDriver) FindMock(repoRoot string) (string, string) {
	hint := "run `make mock` to install the pinned version (tests/e2e/harness/CURSOR_MOCK_VERSION) into .bin/"
	if p := os.Getenv("A10N_CURSOR_MOCK"); p != "" {
		return p, hint
	}
	if p := filepath.Join(repoRoot, ".bin", "a10n-cursor-mock"); fileExists(p) {
		return p, hint
	}
	if p, err := exec.LookPath("a10n-cursor-mock"); err == nil {
		return p, hint
	}
	return "", hint
}

func (cursorDriver) unsupported(a Action, why string) error {
	return &UnsupportedError{Harness: "cursor", Step: fmt.Sprintf("%s (kind %d): %s", a.ID, a.Kind, why)}
}

// cursorTool is the assistant line of one tool call, with the call's id.
func cursorLine(content ...map[string]any) string {
	return codexLine(content...) // the scenario protocol is the same on every harness
}

// cursorWorkspaceMark stands for the workspace root in a rendered line; the script swaps in $PWD.
const cursorWorkspaceMark = "@@WORKSPACE@@"

// cursorPassthrough are the tools a generic ToolUse may name: those the mock runs with string inputs.
var cursorPassthrough = map[string]bool{"Read": true, "Grep": true, "Delete": true, "Shell": true}

func (c cursorDriver) render(a Action) (string, error) {
	switch a.Kind {
	case ActWrite:
		return cursorLine(codexTool(a.ID, "Write", map[string]any{"file_path": a.Path, "content": a.Content})), nil
	case ActEdit:
		return cursorLine(codexTool(a.ID, "Edit", map[string]any{"file_path": a.Path, "old_string": a.Old, "new_string": a.New})), nil
	case ActBash:
		return cursorLine(codexTool(a.ID, "Bash", map[string]any{"command": a.Command})), nil
	case ActSay:
		return cursorLine(codexText(a.Text)), nil
	case ActSayWrite:
		return cursorLine(codexText(a.Text), codexTool(a.ID, "Write", map[string]any{"file_path": a.Path, "content": a.Content})), nil
	case ActSayBash:
		return cursorLine(codexText(a.Text), codexTool(a.ID, "Bash", map[string]any{"command": a.Command})), nil
	case ActDispatch:
		if a.Isolation != "" {
			return "", c.unsupported(a, "a Task has no worktree option (subagent-worktree-isolation)")
		}
		return cursorLine(codexTool(a.ID, "Task", map[string]any{
			"description": "delegated work", "prompt": a.Text, "subagent_type": "generalPurpose", "script": a.Script})), nil
	case ActCompact:
		if a.UnwrittenParent {
			return "", c.unsupported(a, "a compaction naming an unwritten parent is Claude's")
		}
		return `{"type":"compact","trigger":"manual"}`, nil
	case ActToolUse:
		if a.Background {
			return "", c.unsupported(a, "a background command's receipt names no task")
		}
		if !cursorPassthrough[a.Tool] {
			return "", c.unsupported(a, "the mock runs no "+a.Tool+" tool")
		}
		in := map[string]any{}
		for k, v := range a.Input {
			in[k] = v
		}
		return cursorLine(codexTool(a.ID, a.Tool, in)), nil
	case ActSkill:
		// Cursor has no skill tool: reading the skill's SKILL.md is how it loads one.
		return cursorLine(codexTool(a.ID, "Read", map[string]any{
			"file_path": cursorWorkspaceMark + "/" + harness.ProjectSkillDirs(cursorharness.New())[0] + "/" + a.Text + "/SKILL.md"})), nil
	case ActBashBatch:
		return "", c.unsupported(a, "several calls in one message are not modelled")
	}
	return "", c.unsupported(a, "a Claude Code record with no Cursor counterpart")
}

// RenderScript renders the scenario as the shell the mock runs. Each step is one unit of
// progress: a tool call or a prompt written to the transcript (a compaction writes it
// again). The script plays the step its progress, counted from where this run began
// (SLOP_BASE, set for a resumed session), names.
func (c cursorDriver) RenderScript(s Scenario) (string, error) {
	var b strings.Builder
	b.WriteString(`set -u
SF="${A10N_MOCK_SESSION_FILE:-/dev/null}"
cnt() { n=$(grep -c "$1" "$SF" 2>/dev/null); echo "${n:-0}"; }
BASE=0; [ "$SF" = "${SLOP_BASE_FILE:-}" ] && BASE=${SLOP_BASE:-0}
PROG=$(( $(cnt '"type":"tool_use"') + $(cnt '"role":"user"') - BASE - 1 ))
`)
	for i, t := range s.turns {
		line, err := c.render(t.act)
		if err != nil {
			return "", err
		}
		if strings.Contains(line, cursorWorkspaceMark) {
			// the line names a path in the workspace: the script runs there, so $PWD is its root
			fmt.Fprintf(&b, "if [ \"$PROG\" -eq %d ]; then\n  printf '%%s\\n' %s | sed \"s|%s|$PWD|g\"\n  exit 0\nfi\n", i, shQuote(line), cursorWorkspaceMark)
			continue
		}
		fmt.Fprintf(&b, "if [ \"$PROG\" -eq %d ]; then\n  printf '%%s\\n' %s\n  exit 0\nfi\n", i, shQuote(line))
	}
	// the mock reads the assistant lines of the stream-json; the final reply is one of them
	// (a result line of the script is not read: the run's result is what the assistant said)
	if s.result != "" {
		fmt.Fprintf(&b, `printf '%%s\n' %s`, shQuote(cursorLine(codexText(s.result))))
	}
	return b.String(), nil
}

// pluginDirs are the directories the run loads with --plugin-dir.
//
// Known mock gap (harness-mocks, cursor-mock): the plugin's hooks name their commands
// relative to the plugin ("./hooks/x.sh"), cursor-agent runs a plugin hook from the plugin's
// directory with CURSOR_PLUGIN_ROOT set, and the mock does neither, so on it those hooks
// do nothing and every test that needs a refusal fails. Not worked around here: fixed in the mock.
func (cursorDriver) pluginDirs(e *Env) []string {
	return []string{filepath.Join(e.repoRoot, "marketplace", "plugins", pluginName)}
}

// Command builds the cursor-mock invocation for one run.
func (c cursorDriver) Command(e *Env, l Launch) *exec.Cmd {
	args := []string{"-p", "--force", "--trust", "--output-format", "stream-json",
		"--script", l.ScriptPath, "--workspace", l.WorkDir}
	for _, d := range c.pluginDirs(e) {
		args = append(args, "--plugin-dir", d)
	}
	switch l.Mode {
	case SessionResume:
		args = append(args, "--resume", e.harnessID(l.SessionID))
	case SessionFork:
		e.t.Skipf("harness cursor: a fork of a conversation is not modelled")
	}
	args = append(args, l.Prompt)
	c.syncPlugins(e)
	cmd := exec.Command(e.mock, args...)
	cmd.Dir = l.WorkDir
	cmd.Env = append(cursorHostEnv(), e.autoWatchEnv()...)
	cmd.Env = append(cmd.Env, "A10N_CURSOR_MOCK_STOP=1",
		"HOME="+e.home,
		"TMPDIR="+e.tmpDir,
		"PATH="+e.shimDir+string(os.PathListSeparator)+e.binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	if e.checkTimeout != "" {
		cmd.Env = append(cmd.Env, "SLOPRAIL_CHECK_TIMEOUT="+e.checkTimeout)
	}
	if l.Mode == SessionResume {
		// Cursor files the conversation under the workspace the run was opened on, which a
		// session started from a subdirectory makes that subdirectory, not the project root.
		if p := c.TranscriptPath(e, l.WorkDir, l.SessionID); fileExists(p) {
			if b, err := os.ReadFile(p); err == nil {
				n := strings.Count(string(b), `"type":"tool_use"`) + strings.Count(string(b), `"role":"user"`)
				// the base is of THIS session's file only: a sub-agent inherits the environment
				// and runs its own script over a transcript of its own, which starts at zero
				cmd.Env = append(cmd.Env, fmt.Sprintf("SLOP_BASE=%d", n), "SLOP_BASE_FILE="+p)
			}
		}
	}
	return cmd
}

// cursorHostEnv is HostEnv without the enclosing Cursor session's identity.
func cursorHostEnv() []string {
	var out []string
	for _, kv := range HostEnv() {
		if strings.HasPrefix(kv, "CURSOR_") || strings.HasPrefix(kv, "CLAUDE_PROJECT_DIR=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// Observe records the session id the mock's first frame names.
func (c cursorDriver) Observe(e *Env, l Launch, output string) {
	c.syncPluginsBack(e)
	if l.Mode == SessionResume && e.harnessIDs[l.SessionID] != "" {
		return
	}
	for _, line := range strings.Split(output, "\n") {
		var f struct {
			Type      string `json:"type"`
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal([]byte(line), &f) == nil && f.Type == "system" && f.SessionID != "" {
			e.setHarnessID(l.SessionID, f.SessionID)
			return
		}
	}
}

func (cursorDriver) RealCommand(e *Env, projDir, prompt string) (*exec.Cmd, error) {
	bin, err := exec.LookPath("cursor-agent")
	if err != nil {
		return nil, fmt.Errorf("no `cursor-agent` on PATH: %v", err)
	}
	cmd := exec.Command(bin, "-p", "--trust", "--", prompt)
	cmd.Dir = projDir
	cmd.Env = append(os.Environ(), "PATH="+e.binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return cmd, nil
}

// InstallPlugins: the plugin under test is passed with --plugin-dir on every run, so
// only the Env's extra plugins are installed here, as the user's local plugins
// (<home>/.cursor/plugins/local/<name>, a copy of the plugin's root, refreshed before each run), each given the
// .cursor-plugin manifest Cursor reads.
func (c cursorDriver) InstallPlugins(e *Env, dir string) {
	e.t.Helper()
	// The install a user runs also puts the plugin's stop and sessionStart hooks (which a Cursor
	// plugin never receives) in the project's .cursor/hooks.json: the same command, from the
	// built binary, pointed at the plugin under test.
	cmd := exec.Command(e.BinPath("sr-session"), "project-hooks", "install", "--dir", dir,
		"--plugin-dir", filepath.Join(e.repoRoot, "marketplace", "plugins", pluginName))
	cmd.Env = append(os.Environ(), "SLOPRAIL_HARNESS=cursor", "HOME="+e.home)
	if out, err := cmd.CombinedOutput(); err != nil {
		e.t.Fatalf("harness: sr-session project-hooks install: %v: %s", err, out)
	}
	local := filepath.Join(e.home, ".cursor", "plugins", "local")
	if err := os.MkdirAll(local, 0o755); err != nil {
		e.t.Fatalf("harness: mkdir %s: %v", local, err)
	}
	for _, p := range e.extraPlugins {
		manifest := filepath.Join(p.root, ".cursor-plugin", "plugin.json")
		if !fileExists(manifest) {
			if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
				e.t.Fatalf("harness: %v", err)
			}
			body, _ := json.Marshal(map[string]string{"name": p.name})
			if err := os.WriteFile(manifest, body, 0o644); err != nil {
				e.t.Fatalf("harness: %v", err)
			}
		}
	}
	c.syncPlugins(e)
}

// syncPlugins copies each extra plugin into <home>/.cursor/plugins/local/<name>, anew. Cursor
// loads a local plugin only from a real directory or a link that stays inside that directory
// (a link pointing elsewhere is rejected, and internal/harness/cursor.Resolve mirrors that), so
// the plugin cannot be linked to the test's temp directory. A test writes into its plugin
// after installing it (EnablePluginShippingStructure), so the copy is refreshed before every run.
func (cursorDriver) syncPlugins(e *Env) {
	e.t.Helper()
	local := filepath.Join(e.home, ".cursor", "plugins", "local")
	for _, p := range e.extraPlugins {
		dst := filepath.Join(local, p.name)
		if err := os.RemoveAll(dst); err != nil {
			e.t.Fatalf("harness: %v", err)
		}
		err := copyTree(p.root, dst)
		if err != nil {
			e.t.Fatalf("harness: copy plugin %s: %v", p.name, err)
		}
	}
}

// HookEnv names the harness outright, as the plugin's own hook wrapper does.
func (c cursorDriver) HookEnv(e *Env, sessionID string) []string {
	c.syncPlugins(e)
	env := []string{"SLOPRAIL_HARNESS=cursor",
		"PATH=" + e.shimDir + string(os.PathListSeparator) + e.binDir + string(os.PathListSeparator) + os.Getenv("PATH")}
	if sessionID != "" {
		// a shell tool's environment names its conversation (recorded, subprocess-session-env)
		env = append(env, "CURSOR_CONVERSATION_ID="+cursorConversationID(e, sessionID))
	}
	return env
}

// CLIEnv is what a sloprail command a test runs itself (runBinEnv) is given on top of the
// host's: the harness it runs as, which the environment alone does not say, and the plugin
// copies it reads.
func (c cursorDriver) CLIEnv(e *Env) []string {
	c.syncPlugins(e)
	return []string{"SLOPRAIL_HARNESS=cursor"}
}

// ConfigEnv also refreshes the plugin copies: the sloprail commands a test runs itself
// (session start, a check) read the plugins from there too.
func (c cursorDriver) ConfigEnv(e *Env) []string {
	c.syncPlugins(e)
	return []string{"SLOPRAIL_HARNESS=cursor"}
}

func (cursorDriver) ShellEnv() string { return "" }

func (cursorDriver) SessionExport(string) string { return "" }

func (cursorDriver) SubagentSessionExport() string { return "" }

// StopPayload is the stop event's payload (a recorded shape: the common fields, and the
// status and loop count of the stop event).
func (c cursorDriver) StopPayload(e *Env, projDir, sessionID string, active bool) string {
	id := e.harnessID(sessionID)
	loop := 0
	if active {
		loop = 1
	}
	payload, _ := json.Marshal(map[string]any{
		"conversation_id": id, "generation_id": id, "session_id": id, "model": "default",
		"hook_event_name": "stop", "cursor_version": "2026.09.28-64d2043",
		"workspace_roots": []string{resolveWorkDir(projDir)}, "user_email": nil,
		"transcript_path": c.TranscriptPath(e, projDir, sessionID), "status": "completed", "loop_count": loop,
	})
	return string(payload)
}

// IdentityPayload is the conversation and the workspace, with no transcript path: null at
// sessionStart and the first events (recorded).
func (cursorDriver) IdentityPayload(e *Env, projDir, sessionID string) string {
	id := e.harnessID(sessionID)
	payload, _ := json.Marshal(map[string]any{
		"conversation_id": id, "session_id": id, "transcript_path": nil,
		"workspace_roots": []string{resolveWorkDir(projDir)},
	})
	return string(payload)
}

func (cursorDriver) StopBlocked(output string) bool {
	return strings.Contains(output, `"followup_message"`)
}

func (cursorDriver) AgentShim(e *Env, projDir string) (string, string) {
	script := "#!/bin/sh\n" +
		"[ -t 0 ] || cat >/dev/null\n" +
		"unset A10N_CURSOR_MOCK_STOP\n" +
		"exec " + shellQuote(e.mock) + " -p --force --trust --output-format stream-json \\\n" +
		"  --script " + shellQuote(filepath.Join(projDir, ".inner-scenario.sh")) + " \\\n" +
		"  --workspace " + shellQuote(projDir) + " \\\n" +
		"  \"launched agent\" </dev/null\n"
	return "cursor-agent", script
}

func (cursorDriver) JudgeShim(s JudgeShim) (string, string) {
	_, body := claudeDriver{}.JudgeShim(s)
	return "cursor-agent", body
}

var cursorNonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// cursorConversationID is the conversation id the harness gave a session, or the stand-in a
// session no mock has run yet is filed under (its transcript path and its shell's variable agree).
func cursorConversationID(e *Env, sessionID string) string {
	if id := e.harnessID(sessionID); id != "" {
		return id
	}
	return "not-yet-run-" + sessionID
}

// SeedRecord is the one line of a Cursor transcript: a user message, in Cursor's own shape.
func (cursorDriver) SeedRecord() string {
	return `{"role":"user","message":{"content":[{"type":"text","text":"<user_query>\nwork\n</user_query>"}]}}`
}

// TranscriptPath is <home>/.cursor/projects/<workspace, non-alphanumerics as "-">/agent-transcripts/<session>/<session>.jsonl.
func (cursorDriver) TranscriptPath(e *Env, projDir, sessionID string) string {
	id := cursorConversationID(e, sessionID)
	project := cursorNonAlnum.ReplaceAllString(strings.TrimPrefix(resolveWorkDir(projDir), "/"), "-")
	return filepath.Join(e.home, ".cursor", "projects", project, "agent-transcripts", id, id+".jsonl")
}

// SubagentRecordPaths: Cursor keeps a sub-agent's transcript as a conversation of its own
// beside the session's, with nothing that names its parent (subagent-transcripts). The
// sessions a test started are known (Env.harnessIDs), so a conversation beside them that
// is none of those is a sub-agent's.
func (c cursorDriver) SubagentRecordPaths(e *Env, projDir, sessionID string) []string {
	root := c.TranscriptPath(e, projDir, sessionID)
	base := filepath.Dir(filepath.Dir(root))
	known := map[string]bool{}
	for _, id := range e.harnessIDs {
		known[id] = true
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []string
	for _, ent := range entries {
		if !ent.IsDir() || known[ent.Name()] {
			continue
		}
		if p := filepath.Join(base, ent.Name(), ent.Name()+".jsonl"); fileExists(p) {
			out = append(out, p)
		}
	}
	return out
}

func (cursorDriver) ForkTranscript(e *Env, cwd, oldSessionID, newSessionID string) {
	e.t.Skipf("harness cursor: a fork of a conversation is not modelled")
}

// rejected walks a completed tool frame for the reason a hook gave when it refused the call.
func cursorRejections(output string) []string {
	var out []string
	for _, line := range strings.Split(output, "\n") {
		var f struct {
			Type     string         `json:"type"`
			Subtype  string         `json:"subtype"`
			ToolCall map[string]any `json:"tool_call"`
		}
		if json.Unmarshal([]byte(line), &f) != nil || f.Type != "tool_call" || f.Subtype != "completed" {
			continue
		}
		for _, b := range f.ToolCall {
			body, _ := b.(map[string]any)
			res, _ := body["result"].(map[string]any)
			rej, _ := res["rejected"].(map[string]any)
			reason, ok := rej["reason"].(string)
			if !ok {
				// a refused file edit is an error result, not a rejection
				ee, _ := res["error"].(map[string]any)
				reason, ok = ee["modelVisibleError"].(string)
			}
			if ok {
				reason, _, _ = strings.Cut(reason, "\n\nAgent note:")
				out = append(out, reason)
			}
		}
	}
	return out
}

func (cursorDriver) Refusals(output string) []string { return cursorRejections(output) }

// ToolResults are the standard output of the shell commands the stream shows ran.
func (cursorDriver) ToolResults(output string) []string {
	var out []string
	for _, line := range strings.Split(output, "\n") {
		var f struct {
			Type     string         `json:"type"`
			Subtype  string         `json:"subtype"`
			ToolCall map[string]any `json:"tool_call"`
		}
		if json.Unmarshal([]byte(line), &f) != nil || f.Type != "tool_call" || f.Subtype != "completed" {
			continue
		}
		for _, b := range f.ToolCall {
			body, _ := b.(map[string]any)
			res, _ := body["result"].(map[string]any)
			ok, _ := res["success"].(map[string]any)
			if so, has := ok["stdout"].(string); has {
				out = append(out, so)
			}
		}
	}
	return out
}

// stopFollowup is a message a stop hook's followup_message gave the agent: the mock (with its
// stop opt-in) records it in the transcript as a user turn, the way the TUI does, and prints
// nothing on the stream. The record cannot tell a follow-up from the prompt of a resumed run (a
// resume reopens the record, dropping its turn_ended: recorded, runs/session-resume), so the
// prompts the test launched are read off in order and every other user record is a follow-up.
// wentOn: the agent answered it.
type stopFollowup struct {
	text   string
	wentOn bool
}

func cursorFollowups(record string, prompts []string) []stopFollowup {
	var out []stopFollowup
	for _, line := range strings.Split(record, "\n") {
		var r struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Message struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &r) != nil {
			continue
		}
		switch {
		case r.Role == "user":
			text := ""
			if len(r.Message.Content) > 0 {
				text = r.Message.Content[0].Text
			}
			if i := strings.Index(text, "<user_query>\n"); i >= 0 {
				text = text[i+len("<user_query>\n"):]
			}
			text = strings.TrimSuffix(text, "\n</user_query>")
			if len(prompts) > 0 && text == strings.TrimSpace(prompts[0]) {
				prompts = prompts[1:] // a prompt the test launched, not a refusal fed back
				continue
			}
			out = append(out, stopFollowup{text: text})
		case r.Role == "assistant" && len(out) > 0:
			out[len(out)-1].wentOn = true
		}
	}
	return out
}

// BlockingErrors are the reasons the Stop hook refused the end of a turn with (the mock's
// followup_message turns). A sub-agent's stop is not modelled.
func (cursorDriver) BlockingErrors(record string, prompts []string, hookEvent string, dedupe bool) []string {
	if hookEvent != "" && hookEvent != "Stop" {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, f := range cursorFollowups(record, prompts) {
		if dedupe && seen[f.text] {
			continue
		}
		seen[f.text] = true
		out = append(out, f.text)
	}
	return out
}

// StopContinuations are the refusals after which the agent went on.
func (cursorDriver) StopContinuations(record string, prompts []string) []string {
	var out []string
	for _, f := range cursorFollowups(record, prompts) {
		if f.wentOn {
			out = append(out, f.text)
		}
	}
	return out
}

func (cursorDriver) SubagentBlockingErrors([]string) []string    { return nil }
func (cursorDriver) AnySubagentBlockingErrors([]string) []string { return nil }
func (cursorDriver) SubagentFeedbackCount([]string) int          { return 0 }

// copyTree copies the files under src into dst, creating directories, replacing files.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode().Perm())
	})
}

// syncPluginsBack copies what the run wrote inside the loaded plugins (a check's ledger in
// its plugin's own folder) back to the plugin's root, where the test reads it.
// JudgeHooksOff: the judge's cursor-agent runs in a scratch workspace of its own, not the
// project's. Cursor discovers project hooks from the workspace, so none of them fire there
// (measured; see sr-agent's cursor_grant.go).
func (cursorDriver) JudgeHooksOff(argv, projDir string) bool {
	lines := strings.Split(argv, "\n")
	for i, l := range lines {
		if l == "--workspace" && i+1 < len(lines) {
			ws := lines[i+1]
			return ws != "" && ws != projDir && ws != resolveWorkDir(projDir)
		}
	}
	return false
}

func (cursorDriver) syncPluginsBack(e *Env) {
	for _, p := range e.extraPlugins {
		_ = copyTree(filepath.Join(e.home, ".cursor", "plugins", "local", p.name), p.root)
	}
}
