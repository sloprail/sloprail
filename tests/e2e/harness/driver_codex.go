package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// codexDriver is the Driver for OpenAI Codex, run through a10n-codex-mock (the
// version pinned in CODEX_MOCK_VERSION, installed by `make mock`): a scripted
// `codex exec --json` whose scenario script prints one assistant step per line
// (the same stream-json the claude mock reads) and which fires the hooks of
// $CODEX_HOME/hooks.json, of <cwd>/.codex/hooks.json and of the plugins
// $CODEX_HOME/config.toml enables.
//
// What Codex has, and the scenario steps that follow from it:
//
//   - the agent's tools are the shell (Bash) and apply_patch: Write is an
//     "Add File" patch, Edit an "Update File" hunk (whole lines only, as a patch
//     is), everything else a step Codex has no tool for is unsupported;
//   - a sub-agent is spawn_agent then wait_agent, with no worktree option;
//   - sessions are named by Codex, not by the caller: the harness's session ids
//     are aliases of the thread ids the runs print (Env.harnessIDs);
//   - the record is the rollout under $CODEX_HOME/sessions/YYYY/MM/DD, a
//     sub-agent's a rollout of its own naming its session in its meta record.
type codexDriver struct{}

func (codexDriver) Name() string { return "codex" }

// Caps: Codex has no Skill tool, no ask-user-question in exec, no worktree hooks
// or isolation, and no receipt that names a background task (spec/capabilities,
// providers.codex of harness-mocks).
func (codexDriver) Caps() []string {
	return []string{CapSubagents, CapPlugins, CapStopHooks, CapForkResumeCompact, CapTranscript, CapSubagentParentLink, CapRecordHoldsToolResults}
}

func (codexDriver) FindMock(repoRoot string) (string, string) {
	hint := "run `make mock` to install the pinned version (tests/e2e/harness/CODEX_MOCK_VERSION) into .bin/"
	if p := os.Getenv("A10N_CODEX_MOCK"); p != "" {
		return p, hint
	}
	if p := filepath.Join(repoRoot, ".bin", "a10n-codex-mock"); fileExists(p) {
		return p, hint
	}
	if p, err := exec.LookPath("a10n-codex-mock"); err == nil {
		return p, hint
	}
	return "", hint
}

// codexLine is a scenario line: what the mock reads from the script's stdout.
func codexLine(content ...map[string]any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]any{"type": "assistant", "message": map[string]any{"content": content}})
	return strings.TrimSpace(b.String())
}

func codexText(text string) map[string]any { return map[string]any{"type": "text", "text": text} }

func codexTool(id, name string, input map[string]any) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
}

// codexPatch is the apply_patch text of one file section.
func codexAddFile(path, content string) string {
	var b strings.Builder
	b.WriteString("*** Begin Patch\n*** Add File: " + path + "\n")
	if content != "" {
		for _, l := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
			b.WriteString("+" + l + "\n")
		}
	}
	b.WriteString("*** End Patch")
	return b.String()
}

func codexUpdateFile(path, oldText, newText string) string {
	var b strings.Builder
	b.WriteString("*** Begin Patch\n*** Update File: " + path + "\n@@\n")
	for _, l := range strings.Split(strings.TrimSuffix(oldText, "\n"), "\n") {
		b.WriteString("-" + l + "\n")
	}
	for _, l := range strings.Split(strings.TrimSuffix(newText, "\n"), "\n") {
		b.WriteString("+" + l + "\n")
	}
	b.WriteString("*** End Patch")
	return b.String()
}

func (codexDriver) unsupported(a Action, why string) error {
	return &UnsupportedError{Harness: "codex", Step: fmt.Sprintf("%s (kind %d): %s", a.ID, a.Kind, why)}
}

// codexBlock is one step a turn is: the line to print, and the marker suffix that
// tells it has gone ("" for the turn's own, "w" for a wait that follows a spawn).
type codexBlock struct {
	line   string
	suffix string
	// afterCall: the line names the agent the call of this id spawned.
	spawnedBy string
	// compact: the line asks for a compaction, which leaves no id: it has gone when the
	// rollout holds more compactions than this run began with, plus the earlier ones of the scenario.
	compact bool
}

// render renders an action as the blocks it is, or says why Codex cannot take it.
func (c codexDriver) render(a Action) ([]codexBlock, error) {
	switch a.Kind {
	case ActWrite:
		if strings.ContainsAny(a.Path, "\r\n") {
			return nil, c.unsupported(a, "an apply_patch path is one line of the patch, so a path holding a line break cannot be named")
		}
		return []codexBlock{{line: codexLine(codexTool(a.ID, "apply_patch", map[string]any{"command": codexAddFile(a.Path, a.Content)}))}}, nil
	case ActEdit:
		if a.Old == "" {
			return nil, c.unsupported(a, "an edit of no text has no patch")
		}
		return []codexBlock{{line: codexLine(codexTool(a.ID, "apply_patch", map[string]any{"command": codexUpdateFile(a.Path, a.Old, a.New)}))}}, nil
	case ActBash:
		return []codexBlock{{line: codexLine(codexTool(a.ID, "Bash", map[string]any{"command": a.Command}))}}, nil
	case ActSay:
		return []codexBlock{{line: codexLine(codexText(a.Text))}}, nil
	case ActSayWrite:
		return []codexBlock{{line: codexLine(codexText(a.Text), codexTool(a.ID, "apply_patch", map[string]any{"command": codexAddFile(a.Path, a.Content)}))}}, nil
	case ActSayBash:
		return []codexBlock{{line: codexLine(codexText(a.Text), codexTool(a.ID, "Bash", map[string]any{"command": a.Command}))}}, nil
	case ActDispatch:
		if a.Isolation != "" {
			return nil, c.unsupported(a, "spawn_agent has no worktree option (subagent-worktree-isolation)")
		}
		return []codexBlock{
			{line: codexLine(codexTool(a.ID, "spawn_agent", map[string]any{"message": a.Text, "script": a.Script}))},
			{suffix: "w", spawnedBy: a.ID},
		}, nil
	case ActSkill:
		return nil, c.unsupported(a, "Codex has no skill tool")
	case ActToolUse:
		if a.Background {
			return nil, c.unsupported(a, "a background command's receipt names no task")
		}
		if path := a.Input["file_path"]; a.Tool == "Read" && path != "" && len(a.Input) == 1 {
			// Codex reads a file through its shell: the Read of a whole file is `cat` of it.
			return []codexBlock{{line: codexLine(codexTool(a.ID, "Bash", map[string]any{"command": "cat " + shQuote(path)}))}}, nil
		}
		return nil, c.unsupported(a, "Codex has only the shell and apply_patch: no "+a.Tool+" tool")
	case ActCompact:
		if a.UnwrittenParent {
			return nil, c.unsupported(a, "a compaction naming an unwritten parent is Claude's")
		}
		return []codexBlock{{line: `{"type":"compact","trigger":"manual"}`, compact: true}}, nil
	case ActBashBatch:
		return nil, c.unsupported(a, "several calls in one message are not modelled")
	}
	return nil, c.unsupported(a, "a Claude Code record with no Codex counterpart")
}

// RenderScript renders the scenario as the shell the mock runs: each step fires once,
// gated on a marker made of its own id being absent from the session's rollout.
func (c codexDriver) RenderScript(s Scenario) (string, error) {
	var b strings.Builder
	b.WriteString("set -u\nSF=\"${A10N_MOCK_SESSION_FILE:-/dev/null}\"\n")
	b.WriteString("SESS=\"$(cat \"$SF\" 2>/dev/null || true)\"\n")
	compacts := 0
	for i, t := range s.turns {
		blocks, err := c.render(t.act)
		if err != nil {
			return "", err
		}
		for _, blk := range blocks {
			marker := fmt.Sprintf("slop-turn-%d-%s", i, t.act.ID)
			if blk.suffix != "" {
				marker = fmt.Sprintf("slop-turn-%d-%s-%s", i, blk.suffix, t.act.ID)
			}
			line := blk.line
			if blk.compact {
				fmt.Fprintf(&b, `if [ "$(printf '%%s' "$SESS" | grep -c '"type":"compacted"')" -le $(( ${SLOP_COMPACTED_BASE:-0} + %d )) ]; then
  printf '%%s\n' %s
  exit 0
fi
`, compacts, shQuote(line))
				compacts++
				continue
			}
			if blk.spawnedBy != "" {
				// A spawn is answered with a receipt naming the agent; waiting for it is
				// what makes the dispatch run to its end before the scenario goes on, as a
				// Claude Agent call does.
				spawnID := fmt.Sprintf("slop-turn-%d-%s", i, blk.spawnedBy)
				wait := codexLine(codexTool(blk.spawnedBy+"-"+marker, "wait_agent", map[string]any{"targets": []string{"@@AGENT@@"}, "timeout_ms": 600000}))
				fmt.Fprintf(&b, `if ! printf '%%s' "$SESS" | grep -q %q; then
  AGENT="$(jq -r --arg id %q 'select(.payload.call_id==$id and .payload.type=="function_call_output")|.payload.output|try (fromjson|.agent_id) catch empty|select(.!=null)' "$SF" | head -1)"
  printf '%%s\n' %s | sed "s|@@AGENT@@|$AGENT|"
  exit 0
fi
`, marker, blk.spawnedBy+"-"+spawnID, shQuote(wait))
				continue
			}
			line = injectCodexMarker(line, t.act.ID, marker)
			fmt.Fprintf(&b, `if ! printf '%%s' "$SESS" | grep -q %q; then
  printf '%%s\n' %s
  exit 0
fi
`, marker, shQuote(line))
		}
	}
	// The scenario's end is the agent's final answer: Codex prints no result frame, its
	// stream (and its rollout) end on the last agent message.
	fmt.Fprintf(&b, `printf '%%s\n' %s`, shQuote(codexLine(codexText(s.result))))
	return b.String(), nil
}

// injectCodexMarker appends the marker to the call's id (the first tool_use id of the
// line); a line with no call carries no id and is left as it is, like a Claude Say.
func injectCodexMarker(line, id, marker string) string {
	if id == "" {
		return line
	}
	needle := `"id":` + jsonStr(id)
	i := strings.Index(line, needle)
	if i < 0 {
		return line
	}
	return line[:i] + `"id":` + jsonStr(id+"-"+marker) + line[i+len(needle):]
}

// Command builds the codex-mock invocation for one run. Codex names its sessions
// itself, so a first run starts one and a repeat resumes the thread the earlier
// run printed (Observe).
func (codexDriver) Command(e *Env, l Launch) *exec.Cmd {
	args := []string{"exec", "--json", "--skip-git-repo-check", "--dangerously-bypass-hook-trust",
		"--script", l.ScriptPath, "-C", l.WorkDir, "-m", "mock-model"}
	switch l.Mode {
	case SessionResume:
		args = append(args, "resume", e.harnessID(l.SessionID))
	case SessionFork:
		args = append(args, "fork", e.harnessID(l.FromSessionID))
	}
	args = append(args, l.Prompt)
	cmd := exec.Command(e.mock, args...)
	cmd.Dir = l.WorkDir
	cmd.Env = append(codexHostEnv(), e.autoWatchEnv()...)
	cmd.Env = append(cmd.Env,
		"HOME="+e.home,
		"CODEX_HOME="+e.configDir,
		"TMPDIR="+e.tmpDir,
		"PATH="+e.shimDir+string(os.PathListSeparator)+e.binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	if e.checkTimeout != "" {
		cmd.Env = append(cmd.Env, "SLOPRAIL_CHECK_TIMEOUT="+e.checkTimeout)
	}
	// A resumed session's script counts compactions from where the rollout already is.
	if l.Mode == SessionResume {
		if p := rolloutPath(e, e.harnessID(l.SessionID)); p != "" {
			if b, err := os.ReadFile(p); err == nil {
				cmd.Env = append(cmd.Env, fmt.Sprintf("SLOP_COMPACTED_BASE=%d", strings.Count(string(b), `"type":"compacted"`)))
			}
		}
	}
	return cmd
}

// codexHostEnv is HostEnv without the enclosing Codex session's identity.
func codexHostEnv() []string {
	var out []string
	for _, kv := range HostEnv() {
		if strings.HasPrefix(kv, "CODEX_") || strings.HasPrefix(kv, "CODEX=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

var codexThread = regexp.MustCompile(`"thread_id":"([^"]+)"`)

// Observe records the thread a run started: the id Codex gave the session.
func (codexDriver) Observe(e *Env, l Launch, output string) {
	if l.Mode == SessionResume && e.harnessIDs[l.SessionID] != "" {
		return
	}
	for _, line := range strings.Split(output, "\n") {
		var ev struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
		}
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Type == "thread.started" && ev.ThreadID != "" {
			e.setHarnessID(l.SessionID, ev.ThreadID)
			return
		}
	}
}

// RealCommand builds the operator's actual `codex exec`.
func (codexDriver) RealCommand(e *Env, projDir, prompt string) (*exec.Cmd, error) {
	bin, err := exec.LookPath("codex")
	if err != nil {
		return nil, fmt.Errorf("no `codex` on PATH: %v", err)
	}
	cmd := exec.Command(bin, "exec", "--skip-git-repo-check", "--", prompt)
	cmd.Dir = projDir
	cmd.Env = append(os.Environ(), "PATH="+e.binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return cmd, nil
}

// InstallPlugins declares the plugin under test, and the Env's extra ones, in
// $CODEX_HOME/config.toml: each in a local marketplace (a directory holding
// .agents/plugins/marketplace.json) and enabled.
func (c codexDriver) InstallPlugins(e *Env, dir string) {
	e.t.Helper()
	mkt := c.marketplace(e, "sloprail-marketplace", pluginName, filepath.Join(e.repoRoot, "marketplace", "plugins", pluginName))
	var b strings.Builder
	fmt.Fprintf(&b, "[marketplaces.%s]\nsource = %q\n\n[plugins.%q]\nenabled = true\n\n", marketplaceName, mkt, pluginKey)
	for _, p := range e.extraPlugins {
		m := c.marketplace(e, p.marketplace, p.name, p.root)
		fmt.Fprintf(&b, "[marketplaces.%s]\nsource = %q\n\n[plugins.%q]\nenabled = true\n\n", p.marketplace, m, p.name+"@"+p.marketplace)
	}
	if err := os.WriteFile(filepath.Join(e.configDir, "config.toml"), []byte(b.String()), 0o644); err != nil {
		e.t.Fatalf("harness: write codex config: %v", err)
	}
	// The project's own copy, as Claude's .claude/settings.json is: the file a project
	// that uses the plugin has committed (the mock reads the user layer only).
	if err := os.MkdirAll(filepath.Join(dir, ".codex"), 0o755); err != nil {
		e.t.Fatalf("harness: mkdir .codex: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".codex", "config.toml"), []byte(b.String()), 0o644); err != nil {
		e.t.Fatalf("harness: write codex project config: %v", err)
	}
}

// marketplace makes a local marketplace listing one plugin, whose files are the
// directory at root (linked, not copied).
func (codexDriver) marketplace(e *Env, name, plugin, root string) string {
	dir, err := os.MkdirTemp("", "slop-codex-mkt-")
	if err != nil {
		e.t.Fatalf("harness: temp marketplace: %v", err)
	}
	e.t.Cleanup(func() { os.RemoveAll(dir) })
	if err := os.MkdirAll(filepath.Join(dir, ".agents", "plugins"), 0o755); err != nil {
		e.t.Fatalf("harness: %v", err)
	}
	// The entry's path reaches the plugin's own directory (relative to the marketplace,
	// so it resolves to the real directory, not through a link): the mock installs a
	// plugin by copying the directory and does not copy a symbolic link, so a link here
	// installed nothing and the plugin's hooks never ran. Resolved when the mock runs, so
	// files a test adds to the plugin first are installed too.
	rel, err := filepath.Rel(dir, root)
	if err != nil {
		e.t.Fatalf("harness: plugin %s path: %v", plugin, err)
	}
	body, _ := json.Marshal(map[string]any{"name": name, "plugins": []any{map[string]any{
		"name": plugin, "source": map[string]any{"source": "local", "path": rel}}}})
	if err := os.WriteFile(filepath.Join(dir, ".agents", "plugins", "marketplace.json"), body, 0o644); err != nil {
		e.t.Fatalf("harness: %v", err)
	}
	return dir
}

// HookEnv is what a hook, or a call made from inside a session, runs with. Codex gives a
// hook no session variable (the session is in its payload); a shell command the agent runs
// has CODEX_THREAD_ID and CODEX_SESSION_ID.
func (codexDriver) HookEnv(e *Env, sessionID string) []string {
	// SLOPRAIL_HARNESS: the plugin's hook wrapper names the harness, and Codex's own markers
	// (a thread id) are not in a hook's environment, so without it a call made as a hook is read as Claude's.
	env := []string{"SLOPRAIL_HARNESS=codex", "CODEX_HOME=" + e.configDir,
		"PATH=" + e.shimDir + string(os.PathListSeparator) + e.binDir + string(os.PathListSeparator) + os.Getenv("PATH")}
	if sessionID != "" {
		id := e.harnessID(sessionID)
		env = append(env, "CODEX_THREAD_ID="+id, "CODEX_SESSION_ID="+id)
	}
	return env
}

func (codexDriver) ConfigEnv(e *Env) []string { return []string{"CODEX_HOME=" + e.configDir} }

// CLIEnv is what a sloprail command a test runs itself (runBinEnv) is given on top of the
// host's: the harness it runs as, which the environment alone does not say (a Codex shell's
// CODEX_THREAD_ID is only there inside a session), and the config dir the mock keeps its
// rollouts in.
func (c codexDriver) CLIEnv(e *Env) []string {
	return append([]string{"SLOPRAIL_HARNESS=codex"}, c.ConfigEnv(e)...)
}

// ShellEnv: the mock's shell tool carries the harness's identity itself.
func (codexDriver) ShellEnv() string { return "" }

func (codexDriver) SessionExport(string) string { return "" }

func (codexDriver) SubagentSessionExport() string { return "" }

// StopPayload is the Stop hook's payload: Codex's common fields and the turn's.
func (codexDriver) StopPayload(e *Env, projDir, sessionID string, active bool) string {
	payload, _ := json.Marshal(map[string]any{
		"session_id": e.harnessID(sessionID), "transcript_path": e.TranscriptPath(projDir, sessionID),
		"cwd": projDir, "hook_event_name": "Stop", "model": "mock-model", "permission_mode": "bypassPermissions",
		"turn_id": "e2e-turn", "stop_hook_active": active, "last_assistant_message": nil,
	})
	return string(payload)
}

// IdentityPayload is the session id and the working directory.
func (codexDriver) IdentityPayload(e *Env, projDir, sessionID string) string {
	payload, _ := json.Marshal(map[string]any{"session_id": e.harnessID(sessionID), "cwd": projDir})
	return string(payload)
}

func (codexDriver) StopBlocked(output string) bool {
	return strings.Contains(output, `"decision":"block"`)
}

// AgentShim is the `codex` a hook-launched agent resolves to: the mock, running the inner scenario.
func (codexDriver) AgentShim(e *Env, projDir string) (string, string) {
	script := "#!/bin/sh\n" +
		"[ -t 0 ] || cat >/dev/null\n" +
		"exec " + shellQuote(e.mock) + " exec --json --skip-git-repo-check --dangerously-bypass-hook-trust \\\n" +
		"  --script " + shellQuote(filepath.Join(projDir, ".inner-scenario.sh")) + " \\\n" +
		"  -C " + shellQuote(projDir) + " -m mock-model \\\n" +
		"  \"launched agent\" </dev/null\n"
	return "codex", script
}

// LargeJudgeModelArgs: size-lg is gpt-6.1-sol, named by Codex's short -m.
func (codexDriver) LargeJudgeModelArgs() (string, string) { return "-m", "gpt-6.1-sol" }

// JudgeShim is the stand-in for the `codex` the judge (sr-agent) runs by name: it answers the
// same prompt line the claude one does, whatever the harness.
func (codexDriver) JudgeShim(s JudgeShim) (string, string) {
	if s.Kind == JudgeShimUsageLimit {
		// codex reports a usage limit on stderr, status 1. The wording is the one sr-agent's
		// classifier already reads for Codex (services/sr-agent/failure.go); no harness-mocks
		// recording holds a real one yet.
		return "codex", `#!/bin/sh
echo call >>"$LEDGER"
echo "ERROR: You've hit your usage limit. Try again later." >&2
exit 1
`
	}
	_, body := claudeDriver{}.JudgeShim(s)
	return "codex", body
}

// rolloutPath is the rollout file of a thread, or "" when there is none.
func rolloutPath(e *Env, threadID string) string {
	if threadID == "" {
		return ""
	}
	found := ""
	_ = filepath.Walk(filepath.Join(e.configDir, "sessions"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(info.Name(), "-"+threadID+".jsonl") {
			found = p
		}
		return nil
	})
	return found
}

// TranscriptPath is the session's rollout; before the session has run there is none, and
// the path where it would be named after the session.
func (codexDriver) TranscriptPath(e *Env, projDir, sessionID string) string {
	if p := rolloutPath(e, e.harnessID(sessionID)); p != "" {
		return p
	}
	return filepath.Join(e.configDir, "sessions", "not-yet-run", sessionID+".jsonl")
}

// SubagentRecordPaths lists the rollouts of the session's sub-agents: those whose meta
// record names the session as theirs and is a sub-agent's.
func (codexDriver) SubagentRecordPaths(e *Env, projDir, sessionID string) []string {
	thread := e.harnessID(sessionID)
	var paths []string
	_ = filepath.Walk(filepath.Join(e.configDir, "sessions"), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasPrefix(info.Name(), "rollout-") {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		first, _, _ := strings.Cut(string(b), "\n")
		var rec struct {
			Payload struct {
				ID           string `json:"id"`
				SessionID    string `json:"session_id"`
				ThreadSource string `json:"thread_source"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(first), &rec) == nil && rec.Payload.ThreadSource == "subagent" && rec.Payload.SessionID == thread {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Strings(paths)
	return paths
}

func (codexDriver) ForkTranscript(e *Env, cwd, oldSessionID, newSessionID string) {
	e.t.Skipf("harness codex: a fork is made by `exec fork` (RunForked), not by seeding a transcript")
}

// OriginRecord is where a rollout begins: the thread id its session_meta opens on. A fork
// is a rollout of its own (its own id), and a sub-agent's names the root only in session_id.
func (codexDriver) OriginRecord(record string) string {
	first, _, _ := strings.Cut(record, "\n")
	var rec struct {
		Type    string `json:"type"`
		Payload struct {
			ID        string `json:"id"`
			SessionID string `json:"session_id"`
		} `json:"payload"`
	}
	if json.Unmarshal([]byte(first), &rec) != nil || rec.Type != "session_meta" {
		return ""
	}
	if rec.Payload.ID != "" {
		return rec.Payload.ID
	}
	return rec.Payload.SessionID
}

var codexRefusal = regexp.MustCompile(`(?s)Command blocked by PreToolUse hook: (.*?)\. Command: `)

// WrittenBytes: an apply_patch "Add File" is a list of "+<line>" rows, so a file it
// creates always ends its last line (an empty one stays empty).
func (codexDriver) WrittenBytes(content string) string {
	if content == "" {
		return ""
	}
	return strings.TrimSuffix(content, "\n") + "\n"
}

// Refusals reads the PreToolUse refusals the mock reports on its error stream.
func (codexDriver) Refusals(output string) []string {
	var out []string
	for _, m := range codexRefusal.FindAllStringSubmatch(output, -1) {
		out = append(out, m[1])
	}
	return out
}

// ToolResults are the outputs of the shell commands the stream shows ran.
func (codexDriver) ToolResults(output string) []string {
	var out []string
	for _, line := range strings.Split(output, "\n") {
		var ev struct {
			Type string `json:"type"`
			Item struct {
				Type   string `json:"type"`
				Output string `json:"aggregated_output"`
			} `json:"item"`
		}
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Type == "item.completed" && ev.Item.Type == "command_execution" {
			out = append(out, ev.Item.Output)
		}
	}
	return out
}

var hookPrompt = regexp.MustCompile(`(?s)^<hook_prompt hook_run_id="([^"]*)">(.*)</hook_prompt>$`)

// rolloutRecord is the part of a rollout line the readers use.
type rolloutRecord struct {
	Type    string `json:"type"`
	Payload struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"payload"`
}

// hookPrompts are the refusals a rollout holds, each with the event it belongs to.
func hookPrompts(record string) (events, reasons []string, assistantAfter []int) {
	for _, line := range strings.Split(record, "\n") {
		var rec rolloutRecord
		if json.Unmarshal([]byte(line), &rec) != nil || rec.Type != "response_item" {
			continue
		}
		p := rec.Payload
		switch {
		case p.Type == "message" && p.Role == "user" && len(p.Content) > 0:
			if m := hookPrompt.FindStringSubmatch(p.Content[0].Text); m != nil {
				run, _, _ := strings.Cut(m[1], ":")
				ev := map[string]string{"stop": "Stop", "subagent-stop": "SubagentStop"}[run]
				events, reasons = append(events, ev), append(reasons, m[2])
				assistantAfter = append(assistantAfter, 0)
			}
		case p.Type == "message" && p.Role == "assistant", p.Type == "function_call":
			for i := range assistantAfter {
				assistantAfter[i]++
			}
		}
	}
	return
}

func (codexDriver) BlockingErrors(record string, _ []string, hookEvent string, dedupe bool) []string {
	events, reasons, _ := hookPrompts(record)
	var out []string
	seen := map[string]bool{}
	for i, r := range reasons {
		if hookEvent != "" && events[i] != hookEvent {
			continue
		}
		if dedupe && seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

// StopContinuations are the Stop refusals after which the agent went on: a step of its own
// followed, or the continued turn reached a later Stop (which refused again). The last refusal
// of a run that ended on it is one the harness gave up on, as at Claude's stop-hook cap.
func (codexDriver) StopContinuations(record string, _ []string) []string {
	events, reasons, after := hookPrompts(record)
	var out []string
	for i, r := range reasons {
		if events[i] != "Stop" {
			continue
		}
		laterStop := false
		for _, ev := range events[i+1:] {
			laterStop = laterStop || ev == "Stop"
		}
		if after[i] > 0 || laterStop {
			out = append(out, r)
		}
	}
	return out
}

func (c codexDriver) SubagentBlockingErrors(records []string) []string {
	return c.AnySubagentBlockingErrors(records)
}

func (c codexDriver) AnySubagentBlockingErrors(records []string) []string {
	var out []string
	for _, r := range records {
		out = append(out, c.BlockingErrors(r, nil, "SubagentStop", true)...)
	}
	return out
}

func (c codexDriver) SubagentFeedbackCount(records []string) int {
	n := 0
	for _, r := range records {
		n += len(c.BlockingErrors(r, nil, "SubagentStop", false))
	}
	return n
}
