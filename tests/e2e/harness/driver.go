package harness

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
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

	// CapSubagentParentLink: a sub-agent's conversation names the session that dispatched
	// it, so what the sub-agent can cite (the user's words, a sibling's tool output) and
	// what the root can cite of it are resolvable. Cursor records no parent anywhere (hook
	// payloads, transcripts and layout hold none: harness-mocks runs/subagent-transcripts).
	CapSubagentParentLink = "subagent-parent-link"

	// CapRecordHoldsToolResults: the session's own record (transcript) holds the tools'
	// results, refusals and sub-agent replies, and tags each tool call with its id. Cursor's
	// transcript holds neither: sloprail keeps outputs in its own store, and a refusal is a
	// hook rejection that never reaches the transcript.
	CapRecordHoldsToolResults = "record-holds-tool-results"

	// CapRecordPreamble: a fresh session's record opens with lines no reader counts as
	// entries (custom-title / mode / last-prompt) and holds the hooks' own records
	// (SessionStart attachments, the Stop hook summary), so an entry's physical line runs
	// past its ordinal. Cursor's transcript is the conversation alone: no preamble, no hook
	// records, a line per entry (harness-mocks cursor-mock session-transcript-file).
	CapRecordPreamble = "record-preamble"
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
	JudgeShimPlain     JudgeShimKind = iota // writes Verdict where the prompt says
	JudgeShimRecording                      // plain, plus the argv recorded to ArgvFile
	JudgeShimCapturing                      // plain, plus the prompt captured under PromptFile
	JudgeShimSlow                           // delays, decides by the prompt, logs to LogFile
)

// JudgeShim parameterises Driver.JudgeShim.
type JudgeShim struct {
	Kind         JudgeShimKind
	Verdict      string
	ArgvFile     string // JudgeShimRecording
	PromptFile   string // JudgeShimCapturing: absolute path
	LogFile      string // JudgeShimSlow
	DelaySeconds int    // JudgeShimSlow
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

	// IdentityPayload is a hook payload that names only the session and the project
	// folder it runs in: no transcript path, so a reader resolves the session's record
	// from the two, as the first hooks of a session make it.
	IdentityPayload(e *Env, projDir, sessionID string) string

	// SeedRecord is the line a transcript is seeded with for a session that has run no turn:
	// a user message, in the harness's own record shape.
	SeedRecord() string
	// TranscriptPath is where the harness keeps a session's root transcript.
	TranscriptPath(e *Env, projDir, sessionID string) string
	// SubagentRecordPaths lists the sub-agent transcripts of a session, sorted.
	SubagentRecordPaths(e *Env, projDir, sessionID string) []string
	// ForkTranscript writes the transcript a re-forked session opens on.
	ForkTranscript(e *Env, cwd, oldSessionID, newSessionID string)

	// Refusals are the PreToolUse refusal reasons in a run's output stream.
	Refusals(output string) []string
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

// ShellEnv is the environment assignments (each followed by a space, or "") a scenario's shell
// command puts before sr-checks to be seen as run by the agent under the selected harness.
func ShellEnv() string { return mustDriver().ShellEnv() }

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
