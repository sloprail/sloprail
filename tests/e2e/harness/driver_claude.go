package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sloprail/sloprail/internal/harness/claudecode"
	"github.com/sloprail/sloprail/internal/harnessmock"
)

// claudeDriver is the Driver for Claude Code, run through a10n-claude-mock: the
// Claude-specific half of this package — stream-json scenario scripts, the mock's
// argv and environment, settings.json plugin wiring, the <config>/projects
// transcript layout and the readers of its records.
type claudeDriver struct{}

func (claudeDriver) Name() string { return "claude" }

func (claudeDriver) Caps() []string {
	return []string{CapSubagents, CapWorktrees, CapPlugins, CapSkills, CapAskUserQuestion,
		CapStopHooks, CapForkResumeCompact, CapBackgroundTasks, CapTranscript, CapSubagentParentLink, CapRecordHoldsToolResults}
}

// RenderScript renders the scenario as the shell the mock runs.
//
// Each turn is gated on its own marker being absent from the conversation so
// far, so re-running the script advances rather than repeating. With every turn
// emitted, the scenario finishes.
//
// The marker carries the TURN'S OWN ID, not its index. A bare index is unique
// only within one scenario, and a session that is Run more than once — which is
// how a test settles something and then comes back to it under the same
// conversation — writes its markers into a transcript the next Run reads. With
// `slop-turn-0` already in the file from the first Run, every turn of the
// second looks like it has already fired and the whole scenario emits nothing:
// silently, with no error, and with the test observing an empty second cycle
// that it reads as "the hook did not run". The id is the test's own, so
// distinct scenarios cannot collide unless they deliberately reuse it.
func (claudeDriver) RenderScript(s Scenario) (string, error) {
	var b strings.Builder
	b.WriteString("set -u\nSF=\"${A10N_MOCK_SESSION_FILE:-/dev/null}\"\n")
	b.WriteString("SESS=\"$(cat \"$SF\" 2>/dev/null || true)\"\n")

	for i, t := range s.turns {
		jsonl, err := renderClaude(t.act)
		if err != nil {
			return "", err
		}
		marker := fmt.Sprintf("slop-turn-%d-%s", i, turnID(jsonl))
		line := injectMarker(jsonl, marker)
		if t.launchedOutput {
			// A background command's receipt names its output file: "Output is
			// being written to: <file>. You will be notified …". The latest one
			// is the command meant.
			fmt.Fprintf(&b, `if ! printf '%%s' "$SESS" | grep -q %q; then
  OUT="$(printf '%%s' "$SESS" | grep -o 'Output is being written to: [^ ]*\.output' | tail -1 | sed 's/.*: //')"
  printf '%%s\n' %s | sed "s|%s|$OUT|"
  exit 0
fi
`, marker, shQuote(line), launchedOutputPlaceholder)
			continue
		}
		if t.launchedTask {
			// The receipt a background launch was answered with names its id:
			// "Command running in background with ID: <id>" for a Bash,
			// "agentId: <id>" for an Agent. The latest one is the task meant.
			fmt.Fprintf(&b, `if ! printf '%%s' "$SESS" | grep -q %q; then
  TASK="$(printf '%%s' "$SESS" | grep -o 'running in background with ID: [A-Za-z0-9_-]*\|agentId: [A-Za-z0-9_-]*' | tail -1 | sed 's/.*: //')"
  printf '%%s\n' %s | sed "s/%s/$TASK/"
  exit 0
fi
`, marker, shQuote(line), launchedTaskPlaceholder)
			continue
		}
		fmt.Fprintf(&b, `if ! printf '%%s' "$SESS" | grep -q %q; then
  printf '%%s\n' %s
  exit 0
fi
`, marker, shQuote(line))
	}
	fmt.Fprintf(&b, `printf '%%s\n' %s`, shQuote(result(s.result)))
	return b.String(), nil
}

// Command builds the claude-mock invocation for one run: the --session-id /
// --resume / --fork-session flags follow the launch's mode.
func (claudeDriver) Command(e *Env, l Launch) *exec.Cmd {
	scriptPath, workDir, prompt := l.ScriptPath, l.WorkDir, l.Prompt
	// The session flag differs by whether this id has been Run before: --session-id for
	// the first (new session), --resume for a repeat (continuation); a fork resumes the
	// old session under a new id.
	var sessionFlags []string
	switch l.Mode {
	case SessionResume:
		sessionFlags = []string{"--resume", l.SessionID}
	case SessionFork:
		sessionFlags = []string{"--resume", l.FromSessionID, "--fork-session", "--session-id", l.SessionID}
	default:
		sessionFlags = []string{"--session-id", l.SessionID}
	}
	args := []string{
		"-p", "--output-format", "stream-json",
		"--script", scriptPath,
		// The directory the session reports, which RunFrom may place below the
		// repository root. The mock echoes this as `cwd` on every hook payload.
		"--project-dir", workDir,
		"--config-dir", e.configDir,
		"--plugin-cache-dir", e.pluginDir,
		// A permission host, which is what offers AskUserQuestion to a
		// non-interactive run (the mock refuses the tool otherwise).
		"--permission-prompt-tool", "stdio",
	}
	args = append(args, sessionFlags...)
	args = append(args, prompt)
	cmd := exec.Command(e.mock, args...)
	cmd.Dir = workDir
	cmd.Env = append(HostEnv(), e.autoWatchEnv()...)
	cmd.Env = append(cmd.Env,
		"HOME="+e.home,
		"CLAUDE_CONFIG_DIR="+e.configDir,
		"CLAUDE_CODE_PLUGIN_CACHE_DIR="+e.pluginDir,
		"CLAUDE_CODE_TMPDIR="+e.tmpDir,
		// The session-identifying and harness-naming variables are the MOCK's to
		// present, not the harness's: the mock takes --session-id (above) and sets
		// CLAUDE_CODE_SESSION_ID on every hook/script env from it, and sets
		// CLAUDECODE=1 + CLAUDE_CODE_ENTRYPOINT=cli on every hook env unconditionally
		// (a10n-claude-mock internal/hooks/invoker.go) — because the mock stands in
		// for Claude Code and must present the environment it presents. sr-agent's
		// harness detection reads CLAUDECODE/CLAUDE_CODE_ENTRYPOINT and REFUSES with
		// ErrNoHarness when neither is set; the mock now supplies them itself, so the
		// harness no longer sets any of the three here. (This used to be a CI-vs-local
		// gotcha: a developer inside Claude Code inherited CLAUDECODE and never saw the
		// gap, CI did not, and a judge test failed in CI with "no supported harness
		// detected" — now moot, the value is the mock's whatever the outer environment.)
		//
		// The plugin invokes `sloprail`; this is how the hook subprocess finds
		// the build under test rather than whatever happens to be installed.
		//
		// The shim dir goes FIRST, ahead of both the build dir and the real
		// PATH. A test whose hook launches an agent must not reach the
		// operator's actual `claude` — see InstallClaudeShim. When no shim was
		// installed the directory is simply empty and this changes nothing.
		"PATH="+e.shimDir+string(os.PathListSeparator)+
			e.binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	// HostEnv above already dropped the outer session's CLAUDE_CODE_EXECPATH. The
	// mock sets its own in every Bash command (as real Claude Code does), so the
	// harness's sr-checks run prefixes clear it: left in, sr-agent's resolveBinary
	// would exec the mock (or the operator's real claude) directly, bypassing the
	// shim dir InstallClaudeShim puts first on PATH.
	// A test that set a blocked-Stop retry cap passes it to the mock. Appended
	// last so it wins over any ambient value; omitted entirely when unset, leaving
	// the mock's own default (8). See the stopBlockCap field's doc.
	if e.stopBlockCap > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("CLAUDE_CODE_STOP_HOOK_BLOCK_CAP=%d", e.stopBlockCap))
	}
	// The session the mock stands in for, which a sub-agent's scenario (written before the
	// session id is known) reads to export CLAUDE_CODE_SESSION_ID as its real Bash has.
	if n := len(sessionFlags); n > 0 {
		cmd.Env = append(cmd.Env, "SR_E2E_SESSION_ID="+sessionFlags[n-1])
	}
	// A test that lowered the check-execution timeout passes it through to the
	// sr-session subprocess the mock launches for each hook. See the
	// checkTimeout field's doc.
	if e.checkTimeout != "" {
		cmd.Env = append(cmd.Env, "SLOPRAIL_CHECK_TIMEOUT="+e.checkTimeout)
	}
	return cmd
}

// RealCommand builds the operator's actual `claude` invocation (see Env.RunReal).
func (claudeDriver) RealCommand(e *Env, projDir, prompt string) (*exec.Cmd, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return nil, fmt.Errorf("no `claude` on PATH: %v", err)
	}
	cmd := exec.Command(bin,
		"-p", "--model", "haiku",
		// The outer cap. The inner agent carries its own, passed by the
		// guardrail's script, because that is where a runaway would spend.
		"--max-budget-usd", "0.20",
		"--allowed-tools", "Write",
		"--", prompt,
	)
	cmd.Dir = projDir
	cmd.Env = append(claudecode.Session(os.Environ()),
		"PATH="+e.binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	return cmd, nil
}

// InstallPlugins writes the project's settings: THIS repo's plugin as a user would
// install it, plus any extra plugins a test enabled (see EnablePluginShippingFileGuard).
//
// There is deliberately no way to add a lifecycle hook from here. A test that
// hand-wired one into settings.json would be arranging wiring no user has, and
// whatever it then proved would be about the harness's arrangement rather than
// about the product — the whole point of driving the mock is that what fires is
// the plugin someone installs. A property that needs a hook point the plugin
// does not register is a gap in the plugin, and belongs in hooks.json.
//
// The EXTRA plugins are a different matter and are allowed: they ship no hooks —
// they ship DECLARATIONS, discovered by the already-installed sloprail plugin's
// own dispatch reading the project's enabledPlugins. That is exactly how a real
// plugin ships a guardrail (026 does the same for the old format via the shipped
// authoring-slop), so enabling one here arranges no wiring a user lacks; it
// installs a second plugin the way a user installs any plugin.
func (c claudeDriver) InstallPlugins(e *Env, dir string) {
	e.t.Helper()

	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		e.t.Fatalf("harness: mkdir .claude: %v", err)
	}
	enabled := map[string]any{pluginKey: true}
	marketplaces := map[string]any{
		marketplaceName: map[string]any{
			"source": map[string]any{"source": "directory", "path": c.localMarketplace(e)},
		},
	}
	// Extra plugins a test enabled — each a directory-sourced marketplace pointing
	// at the plugin's own install root, exactly as a locally-developed plugin is
	// resolved (internal/harness's directory-source branch).
	for _, p := range e.extraPlugins {
		enabled[p.name+"@"+p.marketplace] = true
		marketplaces[p.marketplace] = map[string]any{
			"source": map[string]any{"source": "directory", "path": p.root},
		}
	}

	settings := map[string]any{
		"enabledPlugins":         enabled,
		"extraKnownMarketplaces": marketplaces,
	}
	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		e.t.Fatalf("harness: encode settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), body, 0o644); err != nil {
		e.t.Fatalf("harness: write settings: %v", err)
	}
}

// localMarketplace returns a directory that is this checkout's marketplace with
// every plugin source made local. The shipped .claude-plugin/marketplace.json
// pins each plugin to a release tag (an object source, for users installing
// from GitHub); a directory-sourced marketplace must resolve plugins to this
// working tree, and the mock reads only string (relative path) sources. So the
// directory holds a rewritten manifest ("./marketplace/plugins/<name>") beside
// a symlink to the checkout's marketplace/ — the plugin's files stay the
// checkout's own, nothing is copied.
func (claudeDriver) localMarketplace(e *Env) string {
	e.t.Helper()
	dir, err := os.MkdirTemp("", "slop-mkt-")
	if err != nil {
		e.t.Fatalf("harness: temp marketplace: %v", err)
	}
	e.t.Cleanup(func() { os.RemoveAll(dir) })
	if err := harnessmock.LocalMarketplace(e.repoRoot, dir); err != nil {
		e.t.Fatalf("harness: %v", err)
	}
	return dir
}

// HookEnv is the environment a hook, or a call made from inside a session, runs with.
func (claudeDriver) HookEnv(e *Env, sessionID string) []string {
	return []string{
		"CLAUDE_CODE_SESSION_ID=" + sessionID, "CLAUDE_CONFIG_DIR=" + e.ConfigDir(),
		"CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=cli", "CLAUDE_CODE_EXECPATH=",
		"PATH=" + e.shimDir + string(os.PathListSeparator) + e.binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
}

// StopBlocked reports whether a Stop's output refuses the turn: the blocking form the
// harness honours.
func (claudeDriver) StopBlocked(output string) bool {
	return strings.Contains(output, `"decision":"block"`)
}

// AgentShim is the `claude` a hook-launched agent resolves to: the mock, running the
// inner scenario.
func (claudeDriver) AgentShim(e *Env, projDir string) (string, string) {
	// sr-agent feeds its prompt on the shim's standard input; the shim answers with
	// a fixed scenario whatever the prompt, so it reads and drops it (a pipe nobody
	// reads would break sr-agent's write), and starts the mock with no stdin: the
	// mock refuses a prompt argument beside a piped stdin.
	script := "#!/bin/sh\n" +
		"[ -t 0 ] || cat >/dev/null\n" +
		"exec " + shellQuote(e.mock) + " \\\n" +
		"  --output-format stream-json \\\n" +
		"  --script " + shellQuote(filepath.Join(projDir, ".inner-scenario.sh")) + " \\\n" +
		"  --project-dir " + shellQuote(projDir) + " \\\n" +
		"  --config-dir " + shellQuote(e.configDir) + " \\\n" +
		"  --plugin-cache-dir " + shellQuote(e.pluginDir) + " \\\n" +
		"  --session-id \"inner-$$\" \\\n" +
		"  \"launched agent\" </dev/null\n"
	return "claude", script
}

// JudgeShim is the stand-in for the `claude` the judge (sr-agent) runs by name.
func (claudeDriver) JudgeShim(s JudgeShim) (string, string) {
	verdict, argvFile, promptPath, logFile, delaySeconds := s.Verdict, s.ArgvFile, s.PromptFile, s.LogFile, s.DelaySeconds
	var script string
	switch s.Kind {
	case JudgeShimPlain:
		// The prompt arrives as the LAST argument (sr-agent passes it positionally
		// after `--`). The shim scans every argument for sr-agent's own
		// "Write your answer to the file <path>" line and writes the verdict there.
		// A here-doc keeps the verdict body intact regardless of its punctuation.
		script = `#!/bin/sh
# Recover the output path sr-agent told the agent to write, from the prompt in
# the arguments. sr-agent appends "Write your answer to the file <path>."
out=""
for arg in "$@"; do
  case "$arg" in
    *"Write your answer to the file "*)
      out="$(printf '%s' "$arg" | sed -n 's/.*Write your answer to the file \([^ ]*\)\. .*/\1/p' | head -1)"
      ;;
  esac
done
if [ -n "$out" ]; then
  cat > "$out" <<'JUDGE_VERDICT_EOF'
` + verdict + `
JUDGE_VERDICT_EOF
fi
exit 0
`
	case JudgeShimRecording:
		script = `#!/bin/sh
# Record the argv this harness was invoked with, one argument per line, so a test
# can assert the judge's --model reached here.
: > ` + shellQuote(argvFile) + `
for arg in "$@"; do
  printf '%s\n' "$arg" >> ` + shellQuote(argvFile) + `
done
# Then behave as the ordinary judge shim: write the verdict to the file sr-agent
# named in the prompt.
out=""
for arg in "$@"; do
  case "$arg" in
    *"Write your answer to the file "*)
      out="$(printf '%s' "$arg" | sed -n 's/.*Write your answer to the file \([^ ]*\)\. .*/\1/p' | head -1)"
      ;;
  esac
done
if [ -n "$out" ]; then
  cat > "$out" <<'JUDGE_VERDICT_EOF'
` + verdict + `
JUDGE_VERDICT_EOF
fi
exit 0
`
	case JudgeShimCapturing:
		// The prompt argument is the one sr-agent appends its answer-file line to and
		// is the whole rendered template; the shim picks that argument and writes it
		// out verbatim, then recovers the output path from it and writes the verdict —
		// exactly as InstallJudgeClaude does.
		script = `#!/bin/sh
out=""
for arg in "$@"; do
  case "$arg" in
    *"Write your answer to the file "*)
      # tail -1, not head -1: if sr-agent retried, the argument carries several
      # "Write your answer to the file <path>" lines (the accumulated attempts),
      # and the CURRENT attempt's path is the LAST one. Writing the verdict to the
      # last path means the current output file is satisfied on the first try, so
      # a well-formed verdict is honored without a retry storm and the captured
      # prompt is a single clean render.
      out="$(printf '%s' "$arg" | sed -n 's/.*Write your answer to the file \([^ ]*\)\. .*/\1/p' | tail -1)"
      printf '%s' "$arg" > ` + shellQuote(promptPath) + `
      # Every prompt, kept apart: judges run concurrently, so which one wrote the file above
      # last is not known; a test that wants one judge's prompt asks for it by a marker.
      mkdir -p ` + shellQuote(promptPath+".d") + ` && printf '%s' "$arg" > ` + shellQuote(promptPath+".d") + `/$$
      # One line per judge call — the prompt's heading — so a test can count how
      # often each judge was asked.
      printf '%s\n' "$arg" | head -1 >> ` + shellQuote(promptPath+".calls") + `
      ;;
  esac
done
if [ -n "$out" ]; then
  cat > "$out" <<'JUDGE_VERDICT_EOF'
` + verdict + `
JUDGE_VERDICT_EOF
fi
exit 0
`
	case JudgeShimSlow:
		script = `#!/bin/sh
out=""
prompt=""
for arg in "$@"; do
  case "$arg" in
    *"Write your answer to the file "*)
      out="$(printf '%s' "$arg" | sed -n 's/.*Write your answer to the file \([^ ]*\)\. .*/\1/p' | tail -1)"
      prompt="$arg"
      ;;
  esac
done
[ -n "$out" ] || exit 0
rule="$(printf '%s' "$prompt" | sed -n 's/.*RULE=\([A-Za-z0-9_-]*\).*/\1/p' | head -1)"
printf '%s %s\n' "$rule" "$(date +%s)" >> ` + shellQuote(logFile) + `
sleep ` + strconv.Itoa(delaySeconds) + `
if printf '%s' "$prompt" | grep -q 'VERDICT-FAIL'; then
  printf '{"pass": false, "reasoning": "JUDGE-NO-%s: the change is wrong"}\n' "$rule" > "$out"
else
  printf '{"pass": true, "reasoning": "fine"}\n' > "$out"
fi
exit 0
`
	}
	return "claude", script
}

// TranscriptPath is where claude keeps a session's transcript: <config>/projects/<encoded project dir>/<session>.jsonl.
func (claudeDriver) TranscriptPath(e *Env, projDir, sessionID string) string {
	return filepath.Join(e.configDir, "projects",
		encodeProjectDir(resolveWorkDir(projDir)), sessionID+".jsonl")
}

// SubagentRecordPaths lists <session>/subagents/agent-*.jsonl beside the transcript.
func (c claudeDriver) SubagentRecordPaths(e *Env, projDir, sessionID string) []string {
	dir := filepath.Join(strings.TrimSuffix(c.TranscriptPath(e, projDir, sessionID), ".jsonl"), "subagents")
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		e.t.Fatalf("harness: read sub-agent records %s: %v", dir, err)
	}
	var paths []string
	for _, ent := range ents {
		name := ent.Name()
		if ent.IsDir() || !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		paths = append(paths, filepath.Join(dir, name))
	}
	sort.Strings(paths)
	return paths
}

// ForkTranscript writes the one-record transcript a re-forked session opens on.
func (claudeDriver) ForkTranscript(e *Env, cwd, oldSessionID, newSessionID string) {
	dir := filepath.Join(e.configDir, "projects", encodeProjectDir(resolveWorkDir(cwd)))
	oldPath := filepath.Join(dir, oldSessionID+".jsonl")
	if _, err := os.Stat(oldPath); err != nil {
		e.t.Fatalf("harness: fork %s: the session being continued has no transcript at %s: %v",
			oldSessionID, oldPath, err)
	}

	// The record the new file continues FROM. The prompt record is the one
	// record every seeded session is guaranteed to have, and it is genuinely in
	// the old file — asserted below rather than assumed, because a fork pointing
	// at a record that is not there resolves to nothing and the test would fail
	// for a reason that has nothing to do with the invariant.
	continued := "e2e-root-" + oldSessionID
	body, err := os.ReadFile(oldPath)
	if err != nil {
		e.t.Fatalf("harness: fork %s: read %s: %v", oldSessionID, oldPath, err)
	}
	if !strings.Contains(string(body), `"uuid":"`+continued+`"`) {
		e.t.Fatalf("harness: fork %s: %s does not hold the record %q the fork would continue from",
			oldSessionID, oldPath, continued)
	}

	// Parentless within its own file AND naming what it continues: both, which
	// is what a real re-forked transcript looks like and what makes the walk
	// take its second hop instead of stopping here.
	line := fmt.Sprintf(
		`{"type":"user","uuid":%q,"parentUuid":null,"logicalParentUuid":%q,"cwd":%q,"message":{"role":"user","content":"continued"}}`+"\n",
		"e2e-fork-"+newSessionID, continued, cwd)

	path := filepath.Join(dir, newSessionID+".jsonl")
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		e.t.Fatalf("harness: fork %s: %v", oldSessionID, err)
	}
}

// blockingErrorsIn reads the refusals out of a record's lines, optionally
// narrowed to one lifecycle event, and optionally without repeats.
func blockingErrorsIn(record, hookEvent string, dedupe bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(record, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec struct {
			Attachment struct {
				Type          string `json:"type"`
				HookEvent     string `json:"hookEvent"`
				BlockingError struct {
					BlockingError string `json:"blockingError"`
				} `json:"blockingError"`
			} `json:"attachment"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Attachment.Type != "hook_blocking_error" {
			continue
		}
		if hookEvent != "" && rec.Attachment.HookEvent != hookEvent {
			continue
		}
		text := rec.Attachment.BlockingError.BlockingError
		// A blocked stop is retried, so the same refusal is recorded once per
		// attempt. What a test asks is which refusals arrived, not how many
		// times the agent was driven round.
		if text != "" && !(dedupe && seen[text]) {
			seen[text] = true
			out = append(out, text)
		}
	}
	return out
}

func stopContinuationsIn(record string) []string {
	var out, pending []string
	ownSummarySeen := false
	for _, line := range strings.Split(record, "\n") {
		var rec struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			IsMeta  bool   `json:"isMeta"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		switch {
		case rec.Type == "user" && rec.IsMeta:
			var text string
			if json.Unmarshal(rec.Message.Content, &text) == nil && strings.HasPrefix(text, "Stop hook feedback:\n") {
				// A later Stop's feedback is itself a later Stop: whatever was
				// pending (with its own summary written) was continued past.
				if ownSummarySeen {
					out = append(out, pending...)
					pending = nil
				}
				pending = append(pending, strings.TrimPrefix(text, "Stop hook feedback:\n"))
				ownSummarySeen = false
			}
		case rec.Type == "system" && rec.Subtype == "stop_hook_summary" && len(pending) > 0 && !ownSummarySeen:
			ownSummarySeen = true // the refused Stop's own summary
		case rec.Type == "assistant", rec.Type == "system" && rec.Subtype == "stop_hook_summary":
			out = append(out, pending...)
			pending = nil
		}
	}
	return out
}

// hookRefusalReason is transcript.HookRefusalReason: the reason of a
// "PreToolUse:<Tool> hook error: <reason>" body, and whether body is one.
func hookRefusalReason(body string) (string, bool) {
	if !strings.HasPrefix(body, "PreToolUse:") {
		return "", false
	}
	_, reason, ok := strings.Cut(body, " hook error: ")
	return reason, ok
}

// resultTexts is a tool_result's content as text: a plain string, or the text
// of each text block in a list.
func resultTexts(raw json.RawMessage) []string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []string{s}
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var out []string
	for _, b := range blocks {
		if b.Type == "text" {
			out = append(out, b.Text)
		}
	}
	return out
}

// Refusals reads the PreToolUse refusals out of the stream's tool_result records (see Result.Refusals).
func (claudeDriver) Refusals(output string) []string {
	var out []string
	for _, line := range strings.Split(output, "\n") {
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &rec) != nil || rec.Type != "user" {
			continue
		}
		var blocks []struct {
			Type    string          `json:"type"`
			IsError bool            `json:"is_error"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(rec.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type != "tool_result" || !b.IsError {
				continue
			}
			for _, body := range resultTexts(b.Content) {
				if reason, ok := hookRefusalReason(body); ok {
					out = append(out, reason)
				}
			}
		}
	}
	return out
}

// ToolResults reads the text of every tool_result block in the stream (see Result.ToolResults).
func (claudeDriver) ToolResults(output string) []string {
	var out []string
	for _, line := range strings.Split(output, "\n") {
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &rec) != nil || rec.Type != "user" {
			continue
		}
		var blocks []struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(rec.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_result" {
				out = append(out, resultTexts(b.Content)...)
			}
		}
	}
	return out
}

// SubagentBlockingErrors reads the SubagentStop refusals the sub-agents were told
// (see Env.SubagentBlockingErrors), from the contents of their transcripts.
func (c claudeDriver) SubagentBlockingErrors(records []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, record := range records {
		fed := map[string]bool{}
		for _, line := range strings.Split(record, "\n") {
			var rec struct {
				Type    string `json:"type"`
				Message struct {
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(line), &rec) == nil && rec.Type == "user" {
				var text string
				if json.Unmarshal(rec.Message.Content, &text) == nil && strings.HasPrefix(text, "Stop hook feedback:\n") {
					fed[strings.TrimPrefix(text, "Stop hook feedback:\n")] = true
				}
			}
			for _, text := range blockingErrorsIn(line, "SubagentStop", true) {
				if fed[text] && !seen[text] {
					seen[text] = true
					out = append(out, text)
				}
			}
		}
	}
	return out
}

// SubagentFeedbackCount counts the "Stop hook feedback" turns in the sub-agents' transcripts.
func (claudeDriver) SubagentFeedbackCount(records []string) int {
	n := 0
	for _, record := range records {
		for _, line := range strings.Split(record, "\n") {
			var rec struct {
				Type    string `json:"type"`
				Message struct {
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			}
			var text string
			if json.Unmarshal([]byte(line), &rec) == nil && rec.Type == "user" &&
				json.Unmarshal(rec.Message.Content, &text) == nil && strings.HasPrefix(text, "Stop hook feedback:\n") {
				n++
			}
		}
	}
	return n
}

// BlockingErrors reads the hook_blocking_error attachments of a transcript.
func (claudeDriver) BlockingErrors(record, hookEvent string, dedupe bool) []string {
	return blockingErrorsIn(record, hookEvent, dedupe)
}

// StopContinuations reads the Stop refusals a transcript shows the agent went on past.
func (claudeDriver) StopContinuations(record string) []string { return stopContinuationsIn(record) }

// AnySubagentBlockingErrors reads every SubagentStop refusal recorded, told or not.
func (claudeDriver) AnySubagentBlockingErrors(records []string) []string {
	var out []string
	for _, record := range records {
		out = append(out, blockingErrorsIn(record, "SubagentStop", true)...)
	}
	return out
}

// FindMock locates the a10n-claude-mock binary the suite drives, or "":
// $A10N_CLAUDE_MOCK (a mock build of your own), then the repo's .bin/ where
// `make mock` installs the pinned version, then PATH.
func (claudeDriver) FindMock(repoRoot string) (string, string) {
	hint := "run `make mock` to install the pinned version (tests/e2e/harness/MOCK_VERSION) into .bin/"
	if p := os.Getenv("A10N_CLAUDE_MOCK"); p != "" {
		return p, hint
	}
	if p := filepath.Join(repoRoot, ".bin", "a10n-claude-mock"); fileExists(p) {
		return p, hint
	}
	if p, err := exec.LookPath("a10n-claude-mock"); err == nil {
		return p, hint
	}
	return "", hint
}

// StopPayload is Claude Code's Stop hook payload.
func (claudeDriver) StopPayload(e *Env, projDir, sessionID string, active bool) string {
	payload, _ := json.Marshal(map[string]any{
		"session_id": sessionID, "transcript_path": e.TranscriptPath(projDir, sessionID),
		"cwd": projDir, "stop_hook_active": active, "hook_event_name": "Stop",
	})
	return string(payload)
}

// ShellEnv names the harness the way the mock's own Bash does.
func (claudeDriver) ShellEnv() string {
	return "CLAUDECODE=1 CLAUDE_CODE_ENTRYPOINT=cli CLAUDE_CODE_EXECPATH= "
}

// SessionExport exports CLAUDE_CODE_SESSION_ID.
func (claudeDriver) SessionExport(sessionID string) string {
	return "export CLAUDE_CODE_SESSION_ID=" + shQuote(sessionID) + "; "
}

// SubagentSessionExport exports the session the run was started under, unless the shell has one.
func (claudeDriver) SubagentSessionExport() string {
	return `export CLAUDE_CODE_SESSION_ID="${CLAUDE_CODE_SESSION_ID:-$SR_E2E_SESSION_ID}"; `
}

// ConfigEnv points a process at the isolated config dir the mock keeps its records in.
func (claudeDriver) ConfigEnv(e *Env) []string { return []string{"CLAUDE_CONFIG_DIR=" + e.configDir} }

// Observe: Claude sessions are named by the caller (--session-id), so there is nothing to learn.
func (claudeDriver) Observe(*Env, Launch, string) {}
