package harness

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/sloprail/sloprail/internal/harness"
)

// Capabilities a Driver may declare. A test that needs one the selected harness
// lacks calls RequireCap and skips, naming it.
const (
	CapSubagents         = "subagents"
	CapWorktrees         = "worktrees"
	CapPlugins           = "plugins"
	CapSkills            = "skills"
	CapAskUserQuestion   = "ask-user-question"
	CapStopHooks         = "stop-hooks"
	CapForkResumeCompact = "fork-resume-compact"
	CapBackgroundTasks   = "background-tasks"
	CapTranscript        = "transcript"

	// CapForkSessions: a session can be forked (RunForked): a new session continuing another's
	// conversation. Cursor's mock does not model a fork of a conversation.
	CapForkSessions = "fork-sessions"

	// CapAllowNotice: the harness can show the person a note on a hook that allows (a
	// systemMessage). Cursor has no field for one: the note goes to stderr only.
	CapAllowNotice = "allow-notice"

	// CapNullTranscriptPath: a hook payload with no transcript path means the session keeps
	// none (Codex --ephemeral). Cursor names none on its first events and sloprail locates
	// the file it will write, so there it is an ordinary payload and guardrails stay on.
	CapNullTranscriptPath = "null-transcript-path"

	// CapRecordHoldsHookContext: the record holds the context a start hook added (the plugin's
	// rules-first text), as it holds the user's words. Cursor writes it nowhere in the
	// transcript: the mock hands it to the scripted agent in A10N_MOCK_ADDITIONAL_CONTEXT
	// (harness-mocks runs/additional-context).
	CapRecordHoldsHookContext = "record-holds-hook-context"

	// CapResumeFromOtherDirectory: a session resumed from another directory keeps appending to
	// the record where it began (Claude Code reports a path under the new directory's project
	// folder, where no file exists). Cursor files a conversation under its workspace folder.
	CapResumeFromOtherDirectory = "resume-from-another-directory"

	// CapScriptedRetryText: a scenario can say something only AFTER a Stop refusal. The
	// cursor-mock plays every step before the first Stop, and the retry says only the
	// scenario's final result.
	CapScriptedRetryText = "scripted-retry-text"

	// CapSubagentParentLink: a sub-agent's conversation names the session that dispatched
	// it, so what the sub-agent can cite (the user's words, a sibling's tool output) and
	// what the root can cite of it are resolvable. Cursor records no parent anywhere (hook
	// payloads, transcripts and layout hold none: harness-mocks runs/subagent-transcripts;
	// a confirmed Cursor bug, https://forum.cursor.com/t/163054). Tests branch on it: where
	// it is absent a sub-agent's citations always error and describe names no link.
	CapSubagentParentLink = "subagent-parent-link"

	// CapRecordHoldsToolResults: the session's own record (transcript) holds the tools'
	// results, refusals and sub-agent replies, and tags each tool call with its id. Cursor's
	// transcript holds neither: sloprail keeps outputs in its own store, and a refusal is a
	// hook rejection that never reaches the transcript.
	CapRecordHoldsToolResults = "record-holds-tool-results"

	// CapOrphanToolResult: the session's record can hold a tool result whose call is not in
	// the record (a scenario step supplying one). Codex's and Cursor's results are the
	// harness's own record of a command it ran, so each has its call.
	CapOrphanToolResult = "orphan-tool-result"

	// CapMCPTools: the agent can call a tool of an MCP server (mcp__<server>__<tool>) and
	// the scenario can state its result (harness-mocks spec/capabilities mcp-tool: claude
	// only, codex and cursor pending).
	CapMCPTools = "mcp-tools"

	// CapPathLineBreaks: the harness's file tool can name a path holding a line break.
	// Codex's apply_patch names a file on one line of the patch, so it cannot.
	CapPathLineBreaks = "path-line-breaks"
	// CapRecordPreamble: a fresh session's record opens with lines no reader counts as
	// entries (custom-title / mode / last-prompt) and holds the hooks' own records
	// (SessionStart attachments, the Stop hook summary), so an entry's physical line runs
	// past its ordinal. Cursor's transcript is the conversation alone: no preamble, no hook
	// records, a line per entry (harness-mocks cursor-mock session-transcript-file).
	CapRecordPreamble = "record-preamble"

	// CapRecordNamesStartDir: the session's record names the directory the session began in,
	// so a hook that reports another folder (the agent `cd`'d into a worktree) is still the
	// same session's. Cursor's transcript names none (only a lossy project slug), and its
	// hooks report the workspace the conversation was opened in, not a shell's directory.
	CapRecordNamesStartDir = "record-names-start-dir"

	// CapRecordAfterSessionStart: a fresh session's record does not exist while the
	// SessionStart hook runs; the hook's own attachment is then its first (origin) entry.
	// Codex opens the rollout with its session_meta when the thread starts, before any hook.
	CapRecordAfterSessionStart = "record-after-session-start"

	// CapScopedToolRules: a judge can be granted a scoped tool rule (Bash(git show:*),
	// WebFetch(domain:...)) and denied one. Codex has no per-tool permission list, only a
	// sandbox, so sr-agent refuses a run whose grant asks for one rather than round it up.
	CapScopedToolRules = "scoped-tool-rules"

	// CapProseWithCallInOneEntry: a turn that says something and calls a tool is ONE entry
	// of the record (a text block beside the tool_use). Codex's rollout writes the message
	// and the call as separate records, so the prose and the call are two entries.
	CapProseWithCallInOneEntry = "prose-with-call-in-one-entry"

	// CapSeveralCallsInOneEntry: one assistant message of the record can hold several tool
	// calls (Claude Code writes the message with every tool_use block). Codex's rollout and
	// Cursor's transcript write a call apiece, so a turn that makes three calls is three entries.
	CapSeveralCallsInOneEntry = "several-calls-in-one-entry"

	// CapDispatchWithoutCallID: a sub-agent can be dispatched by a tool call that names no id
	// (Claude Code's sub-agent meta then records an empty toolUseId). Codex and Cursor give every
	// call an id, and name the dispatch's link (or none) in their own records.
	CapDispatchWithoutCallID = "dispatch-without-call-id"

	// CapCompactionNamesParent: a compaction's boundary record names the record it continues
	// (Claude Code's logicalParentUuid), which a preserved-segment compaction can leave naming
	// a record no file holds. Codex's compaction is a record in the same rollout and Cursor's
	// leaves no boundary at all: neither names a parent.
	CapCompactionNamesParent = "compaction-names-parent"

	// CapJudgeWritesProject: the agent a check launches in judge mode (sr-agent without
	// --agent-run) can write the project, so its edits reach the project's other rules. A
	// Cursor judge is confined by the engine to its answer file; a Codex judge runs in a
	// read-only sandbox. A test about a launched agent's reach into the project launches it
	// in judge mode where this is declared and with --agent-run where not (ForHarness's
	// {{agent-mode}}).
	CapJudgeWritesProject = "judge-writes-project"

	// CapJudgeTreeInArgv: the project a judge is asked about reaches the harness as an
	// argument (Claude Code's --add-dir). A Codex judge reads it by absolute path under a
	// sandbox and a Cursor judge in an empty workspace, so neither launch has an argument
	// naming the tree: there the prompt's "The project being judged is at <dir>." is the
	// only place the tree is named.
	CapJudgeTreeInArgv = "judge-tree-in-argv"

	// CapShellDenyBesideGrant: a judge can be granted a shell command and denied a form of it
	// in the same run. Cursor does not enforce a shell deny beside a shell grant (measured), so
	// sr-agent refuses such a run rather than promise a confinement it cannot give.
	CapShellDenyBesideGrant = "shell-deny-beside-grant"

	// CapSubagentLifecycleHooks: the harness fires a sub-agent's start and stop hooks, which is
	// what feeds the session's sub-agent registry (`sr-session agents list`). Cursor's
	// subagentStart/subagentStop never fire in print mode (harness-mocks runs/subagent-lifecycle-hooks),
	// so its registry stays empty.
	CapSubagentLifecycleHooks = "subagent-lifecycle-hooks"
)

// SessionMode is how a launch relates to the session id it names.
type SessionMode int

const (
	// SessionNew starts a session under SessionID.
	SessionNew SessionMode = iota
	// SessionResume continues the existing session SessionID, appending the prompt.
	SessionResume
	// SessionFork starts SessionID as a fork of FromSessionID.
	SessionFork
)

// Launch is everything a Driver needs to build the command that runs the agent
// once: what to run (ScriptPath), where (ProjDir holds the transcript's project,
// WorkDir is the directory the session reports) and under which session.
type Launch struct {
	ProjDir       string
	WorkDir       string
	ScriptPath    string
	Prompt        string
	Mode          SessionMode
	SessionID     string
	FromSessionID string
}

// JudgeShimKind selects which stand-in for the judge's agent binary to install.
type JudgeShimKind int

const (
	JudgeShimPlain      JudgeShimKind = iota // writes Verdict where the prompt says
	JudgeShimRecording                       // plain, plus the argv recorded to ArgvFile
	JudgeShimCapturing                       // plain, plus the prompt captured under PromptFile
	JudgeShimSlow                            // delays, decides by the prompt, logs to LogFile
	JudgeShimScript                          // Body as it is: a test's own stand-in, under the harness's binary name
	JudgeShimUsageLimit                      // counts its calls in $LEDGER and dies the way the harness does at a usage limit
)

// JudgeShim parameterises Driver.JudgeShim.
type JudgeShim struct {
	Kind         JudgeShimKind
	Verdict      string
	ArgvFile     string // JudgeShimRecording
	PromptFile   string // JudgeShimCapturing: absolute path
	LogFile      string // JudgeShimSlow
	DelaySeconds int    // JudgeShimSlow
	Body         string // JudgeShimScript
}

// Driver is everything in the e2e harness that is specific to one agent
// harness (Claude Code today): how the scripted agent is launched and what it
// is told to do, how a plugin is installed, where its record lives and how that
// record and the run's stream are read. Env and Result delegate to it, so a test
// is written once against Env and runs against whichever harness SR_HARNESS names.
type Driver interface {
	// Name is the value of SR_HARNESS that selects this driver.
	Name() string
	// Caps lists the capabilities (Cap*) this harness has.
	Caps() []string

	// FindMock locates the harness's mock agent binary for the repo at repoRoot, or
	// "" with what to run to install it.
	FindMock(repoRoot string) (path, installHint string)
	// StopPayload is the payload the harness hands the Stop hook at the end of a
	// turn; active is stop_hook_active, true for the retry after a refusal.
	StopPayload(e *Env, projDir, sessionID string, active bool) string
	// ShellEnv is the environment assignments (each followed by a space, or "") a
	// scenario's shell command puts before sr-checks to be seen as run by the agent:
	// what the harness would have exported to it, where the mock does not.
	ShellEnv() string
	// SessionExport is the shell (ending "; ", or "") exporting the session's id to
	// the commands that follow, as a real agent's shell has it.
	SessionExport(sessionID string) string
	// SubagentSessionExport is SessionExport for a sub-agent's scenario, whose
	// session is not known when it is written: it reads it from the run's own env.
	SubagentSessionExport() string

	// Observe is told what a run printed, once it has ended, for a harness that names
	// the session itself (the caller's session id is then an alias of the harness's).
	Observe(e *Env, l Launch, output string)
	// ConfigEnv is what points a process at the isolated config dir the mock keeps
	// its records in, for a call made outside any session.
	ConfigEnv(e *Env) []string

	// RenderScript renders a scenario as the script the launched agent runs; a step
	// this harness cannot take is an *UnsupportedError.
	RenderScript(s Scenario) (string, error)
	// Command builds the command that runs the agent once: argv and environment.
	Command(e *Env, l Launch) *exec.Cmd
	// RealCommand builds the command that drives the operator's real, billed agent.
	RealCommand(e *Env, projDir, prompt string) (*exec.Cmd, error)

	// InstallPlugins writes the project's plugin wiring: the plugin under test
	// plus the Env's extra plugins, enabled the way a user would.
	InstallPlugins(e *Env, dir string)
	// HookEnv is the environment a hook, or a call made from inside a session, runs with.
	HookEnv(e *Env, sessionID string) []string

	// AgentShim is the executable (file name, body) a hook-launched agent resolves
	// to, running the scenario written beside projDir by InnerScenario.
	AgentShim(e *Env, projDir string) (name, body string)
	// JudgeShim is the executable (file name, body) standing in for the judge's
	// agent binary.
	JudgeShim(s JudgeShim) (name, body string)
	// JudgeHooksOff reports whether the argv the judge's agent was launched with (one
	// argument per line) keeps the project's and plugins' hooks from running in it.
	JudgeHooksOff(argv, projDir string) bool

	// LargeJudgeModelArgs is the flag and value a judge asking for size-lg reaches the
	// harness's argv with.
	LargeJudgeModelArgs() (flag, value string)
	// MediumJudgeModelArgs is the same for size-md, the judge default.
	MediumJudgeModelArgs() (flag, value string)
	// JudgeAccess reads the recording of a JudgeShimRecording judge into what the judge was
	// granted (see JudgeAccess).
	JudgeAccess(argvFile string) (JudgeAccess, error)

	// SkillDir is the project-relative directory this harness reads a project's skills
	// from (<dir>/<name>/SKILL.md).
	SkillDir() string

	// IdentityPayload is a hook payload that names only the session and the project
	// folder it runs in: no transcript path, so a reader resolves the session's record
	// from the two, as the first hooks of a session make it.
	IdentityPayload(e *Env, projDir, sessionID string) string

	// SeedTranscript gives a session that has run no turn the record its agent would have (a
	// user message, in the harness's own shape): a refusal reached without a transcript is stored
	// for no key. A harness that names its own sessions has no path to write at before a turn
	// ran, so it seeds under an id of its own.
	SeedTranscript(e *Env, projDir, sessionID string)
	// TranscriptPath is where the harness keeps a session's root transcript.
	TranscriptPath(e *Env, projDir, sessionID string) string
	// RecordLayout is what a fresh record holds ahead of the first prompt: the lines the
	// harness's own preamble takes (the mock's, not entries) and the records a session's
	// start leaves before it. A test names the line the first prompt sits on, in
	// `#skip <line>` or a (<record>:L-L) reference, authored before the run.
	RecordLayout() (preambleLines, sessionStartRecords int)
	// NextPromptLine is the 1-based physical line a prompt a resume appends to record
	// (the record's current text) will sit on.
	NextPromptLine(record string) int
	// SubagentRecordPaths lists the sub-agent transcripts of a session, sorted.
	SubagentRecordPaths(e *Env, projDir, sessionID string) []string
	// ForkTranscript writes the transcript a re-forked session opens on.
	ForkTranscript(e *Env, cwd, oldSessionID, newSessionID string)

	// SubagentRecordPath is where the record of the session's sub-agent agentID is (or will
	// be) kept, in the layout this harness ties a sub-agent's record to its parent by.
	// Empty for a harness with no such tie (no CapSubagentParentLink: Cursor's sub-agent is
	// a conversation of its own that names no parent).
	SubagentRecordPath(e *Env, projDir, sessionID, agentID string) string
	// ForgeSubagentRecord writes, at SubagentRecordPath, the record of a sub-agent working
	// in cwd that was given prompt, in this harness's own record shape. It writes nothing
	// and returns "" where SubagentRecordPath is empty.
	ForgeSubagentRecord(e *Env, projDir, sessionID, agentID, cwd, prompt string) string
	// SubagentHookPayload is the payload of one of the sub-agent's hooks (event is the
	// harness-neutral name: PreToolUse, SubagentStop) in this harness's own field shape.
	SubagentHookPayload(e *Env, projDir, sessionID, agentID, event, cwd string, extra map[string]any) string
	// ForgeBareTranscript writes a session record holding one user prompt and nothing else
	// (no hook ever ran for it) where this harness keeps the session's, and returns its path.
	ForgeBareTranscript(e *Env, projDir, sessionID string) string
	// Companions are the files this harness keeps for a session beside its record, as an
	// archive of the session keeps them: archive-relative path -> content. A harness whose
	// companions are written by its own hooks (Cursor's tool outputs) reads them; one whose
	// are the harness's own (Claude Code's tool results) plants them; one with none returns nil.
	Companions(e *Env, projDir, sessionID string) map[string]string

	// SkillLoadTool is the tool whose call loads a skill: the Skill tool where the harness
	// has one, else the tool that reads the skill's SKILL.md (CapSkills names the former).
	SkillLoadTool() string

	// WrittenBytes is what the harness's file-writing tool leaves on disk when the agent
	// writes content: the content itself, unless the tool shapes it (Codex's apply_patch
	// ends every non-empty file with a newline).
	WrittenBytes(content string) string
	// ResultRecord is the line of a session record (the text of its file) that holds the
	// result of the tool call the scenario named id, "" when it holds none.
	ResultRecord(record, id string) string
	// Refusals are the PreToolUse refusal reasons in a run's output stream.
	Refusals(output string) []string
	// RefusalOutput is the output stream of a run in which a PreToolUse hook refused the
	// agent's tool call with reason, in this harness's own shape: what Refusals reads back.
	RefusalOutput(reason string) string
	// ToolResults are the tool_result texts in a run's output stream.
	ToolResults(output string) []string
	// StopBlocked reports whether a Stop hook's output refuses the turn.
	StopBlocked(output string) bool
	// BlockingErrors are the blocking hook errors in a transcript, optionally
	// narrowed to one lifecycle event and optionally without repeats. prompts are the
	// prompts the test launched the session with, in order: a harness whose record does
	// not tell a prompt from a refusal fed back as one (Cursor's) reads them to.
	BlockingErrors(record string, prompts []string, hookEvent string, dedupe bool) []string
	// StopContinuations are the reasons of Stop refusals the agent went on past.
	StopContinuations(record string, prompts []string) []string
	// SubagentBlockingErrors are the SubagentStop refusals the sub-agents were
	// actually told, read from the contents of their own transcripts.
	SubagentBlockingErrors(records []string) []string
	// AnySubagentBlockingErrors are the SubagentStop refusals recorded, told or not.
	AnySubagentBlockingErrors(records []string) []string
	// SubagentFeedbackCount counts the refusal-feedback turns in sub-agent transcripts.
	SubagentFeedbackCount(records []string) int
	// SubagentReply is the text the root record at path holds of the reply the
	// sub-agent dispatched by the call whose id starts with callID handed back.
	SubagentReply(path, callID string) (string, error)
}

// harnessEnvVar selects the Driver.
const harnessEnvVar = "SR_HARNESS"

// knownHarnesses are the names SR_HARNESS may carry; only the ones with a
// driver in drivers() can run.
var knownHarnesses = []string{"claude", "codex", "cursor"}

func drivers() map[string]Driver {
	return map[string]Driver{"claude": claudeDriver{}, "codex": codexDriver{}, "cursor": cursorDriver{}}
}

var (
	selectOnce   sync.Once
	selected     Driver
	selectionErr error
)

// selectDriver is the one place SR_HARNESS is read: unset means claude. A name
// with no driver is an error, never a fall-back to claude.
func selectDriver() (Driver, error) {
	selectOnce.Do(func() {
		name := strings.TrimSpace(os.Getenv(harnessEnvVar))
		if name == "" {
			name = "claude"
		}
		if d, ok := drivers()[name]; ok {
			selected = d
			return
		}
		for _, k := range knownHarnesses {
			if k == name {
				selectionErr = fmt.Errorf("harness: %s=%s has no e2e driver yet (implemented: %s)", harnessEnvVar, name, implemented())
				return
			}
		}
		selectionErr = fmt.Errorf("harness: unknown %s=%q (known: %s; implemented: %s)", harnessEnvVar, name, strings.Join(knownHarnesses, ", "), implemented())
	})
	return selected, selectionErr
}

func implemented() string {
	var names []string
	for n := range drivers() {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// ShellEnv is the environment assignments (each followed by a space, or "") a command the
// agent runs in its shell is prefixed with to run as the current harness's session.
func ShellEnv() string { return mustDriver().ShellEnv() }

// mustDriver is the selected Driver for code with no *testing.T at hand. New has
// already failed the test on a bad selection, so reaching the panic means a
// helper ran without an Env.
func mustDriver() Driver {
	d, err := selectDriver()
	if err != nil {
		panic(err.Error())
	}
	return d
}

// SkillDir is the project-relative directory the selected harness reads a project's
// skills from.
func SkillDir(t testing.TB) string {
	t.Helper()
	d, err := selectDriver()
	if err != nil {
		t.Fatal(err)
	}
	return d.SkillDir()
}

// Selected is the name of the harness SR_HARNESS selects.
func Selected(t testing.TB) string {
	t.Helper()
	d, err := selectDriver()
	if err != nil {
		t.Fatal(err)
	}
	return d.Name()
}

// HasCap reports whether the selected harness declares the capability, without skipping.
func HasCap(t testing.TB, cap string) bool {
	t.Helper()
	d, err := selectDriver()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range d.Caps() {
		if c == cap {
			return true
		}
	}
	return false
}

// OwnTree is the isolation a dispatch gets when a test asks for the sub-agent's own tree:
// "worktree" where the harness isolates its sub-agents (CapWorktrees), "" where it has no such
// option (Codex's spawn_agent, Cursor's Task) and the sub-agent works in the root's tree. A test
// that needs a tree of its own asks for this and branches on HasCap(CapWorktrees) for what it
// then asserts.
func OwnTree(t testing.TB) string {
	t.Helper()
	if HasCap(t, CapWorktrees) {
		return "worktree"
	}
	return ""
}

// RequireCap skips the test unless the selected harness has every capability named.
func RequireCap(t testing.TB, caps ...string) {
	t.Helper()
	d, err := selectDriver()
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, c := range d.Caps() {
		have[c] = true
	}
	for _, c := range caps {
		if !have[c] {
			t.Skipf("harness %s lacks capability %q", d.Name(), c)
		}
	}
}

// ProjectSkillDir is the project-relative directory the selected harness loads a project's
// own skills from (".claude/skills" on Claude Code), where a test that seeds a skill puts it.
func ProjectSkillDir(t testing.TB) string {
	t.Helper()
	return harness.ProjectSkillDirs(harness.Select([]string{harness.SelectEnv + "=" + Selected(t)}))[0]
}

// SkillLoadTool is the tool the selected harness's PreToolUse names when the agent loads a
// skill: "Skill" on Claude Code, the tool that reads the skill's SKILL.md elsewhere.
func SkillLoadTool(t testing.TB) string {
	t.Helper()
	d, err := selectDriver()
	if err != nil {
		t.Fatal(err)
	}
	return d.SkillLoadTool()
}
