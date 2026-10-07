package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runCLI drives the root command with --dry-run, so the whole flag/resolution
// path runs without spawning a harness. Stdout and stderr are captured
// separately, because keeping the agent's answer out of the diagnostics is
// itself a property worth testing.
//
// resolveBinary reads CLAUDE_CODE_EXECPATH straight from the real process
// environment (via os.Getenv, same as ResolveHarness's own detection), so a
// test run FROM INSIDE a live Claude Code session — this one included — would
// otherwise see its own session's real CLAUDE_CODE_EXECPATH leak into these
// dry-run assertions and print the parent's actual binary path instead of the
// bare "claude" every assertion below expects. t.Setenv clears it (and the two
// detect variables, since --harness=claude above already picks the
// harness explicitly and does not need them either) so these tests keep the
// property the package comment above promises: passing the same whether run in
// CI or inside a session.
func runCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Setenv("CLAUDE_CODE_EXECPATH", "")
	t.Setenv("CLAUDECODE", "")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "")
	cmd := newRoot()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// --harness is passed everywhere below so these tests do not depend on the
// environment they happen to run in — they must pass in CI as well as inside a
// Claude Code session.
const underClaude = "--harness=claude"

// --- prompt --------------------------------------------------------------

func TestCLI_PositionalPrompt(t *testing.T) {
	stdout, _, err := runCLI(t, underClaude, "--model", "size-md", "--dry-run", "is this right?")
	require.NoError(t, err)
	assert.Equal(t, `claude -p --model sonnet `+claudeSettingsArg+` -- "is this right?"`, strings.TrimSpace(stdout))
}

// cursor-agent takes `[prompt...]`, so unquoted words must join rather than
// becoming a wrong-arity error.
func TestCLI_PositionalPromptWordsJoin(t *testing.T) {
	stdout, _, err := runCLI(t, underClaude, "--model", "size-md", "--dry-run", "is", "this", "right?")
	require.NoError(t, err)
	assert.Contains(t, stdout, `"is this right?"`)
}

func TestCLI_PromptFlag(t *testing.T) {
	stdout, _, err := runCLI(t, underClaude, "--model", "size-md", "--dry-run", "--prompt", "from a file")
	require.NoError(t, err)
	assert.Contains(t, stdout, `"from a file"`)
}

// Both forms disagree about what to ask, so picking either would ask a question
// the author did not write.
func TestCLI_BothPromptFormsIsRefused(t *testing.T) {
	_, _, err := runCLI(t, underClaude, "--model", "size-md", "--prompt", "one", "two")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--prompt")
}

func TestCLI_NoPromptIsRefused(t *testing.T) {
	_, _, err := runCLI(t, underClaude, "--model", "size-md")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no prompt")
}

func TestCLI_WhitespaceOnlyPromptIsRefused(t *testing.T) {
	_, _, err := runCLI(t, underClaude, "--model", "size-md", "--prompt", "   ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no prompt")
}

// --- model --------------------------------------------------------------

func TestCLI_MissingModelIsRefused(t *testing.T) {
	_, _, err := runCLI(t, underClaude, "--dry-run", "q")
	require.ErrorIs(t, err, ErrEmptyModelSet)
}

func TestCLI_ModelSetResolvingToNothingIsRefused(t *testing.T) {
	_, stderr, err := runCLI(t, underClaude, "--model", "gpt-5,gemini-2", "--dry-run", "q")
	require.ErrorIs(t, err, ErrNoModelAvailable)
	assert.NotContains(t, stderr, "harness claude, model",
		"nothing may be reported as chosen when the set matched nothing")
}

// The unreachable-entry warning must actually reach the user.
func TestCLI_ReportsUnreachableEntries(t *testing.T) {
	_, stderr, err := runCLI(t, underClaude, "--model", "size-md,claude-opus-5", "--dry-run", "q")
	require.NoError(t, err)
	assert.Contains(t, stderr, "claude-opus-5")
	assert.Contains(t, stderr, "never be reached")
}

func TestCLI_ReportsSkippedEntries(t *testing.T) {
	_, stderr, err := runCLI(t, underClaude, "--model", "gpt-5,size-md", "--dry-run", "q")
	require.NoError(t, err)
	assert.Contains(t, stderr, "skipped gpt-5")
}

// Diagnostics must not contaminate the agent's answer: a hook capturing stdout
// to feed a rule must not find advice about model sets in it.
func TestCLI_DiagnosticsGoToStderrOnly(t *testing.T) {
	stdout, stderr, err := runCLI(t, underClaude, "--model", "gpt-5,size-md,claude-opus-5", "--dry-run", "q")
	require.NoError(t, err)

	assert.NotContains(t, stdout, "sr-agent:")
	assert.NotContains(t, stdout, "skipped")
	assert.NotContains(t, stdout, "never be reached")
	assert.Contains(t, stderr, "sr-agent:")
	assert.Equal(t, "claude -p --model sonnet "+claudeSettingsArg+" -- q", strings.TrimSpace(stdout))
}

// --- harness -------------------------------------------------------------

// A dash-leading prompt is refused, as `claude` refuses one — but the refusal
// must name both escapes, or an author who asked an ordinary question of a
// judge is left guessing.
func TestCLI_DashLeadingPromptErrorNamesBothEscapes(t *testing.T) {
	_, _, err := runCLI(t, underClaude, "--model", "size-md", "--not-a-flag is my question")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--prompt")
	assert.Contains(t, err.Error(), "--", "the -- separator must be named")
}

// And both escapes must actually work, or the advice is wrong.
func TestCLI_DashLeadingPromptWorksViaBothEscapes(t *testing.T) {
	viaSeparator, _, err := runCLI(t, underClaude, "--model", "size-md", "--dry-run", "--", "--not-a-flag is my question")
	require.NoError(t, err)
	assert.Contains(t, viaSeparator, `"--not-a-flag is my question"`)

	viaFlag, _, err := runCLI(t, underClaude, "--model", "size-md", "--dry-run", "--prompt", "--not-a-flag is my question")
	require.NoError(t, err)
	assert.Contains(t, viaFlag, `"--not-a-flag is my question"`)
}

func TestCLI_UnknownHarnessIsRefused(t *testing.T) {
	_, _, err := runCLI(t, "--harness", "no-such-harness", "--model", "size-md", "--dry-run", "q")
	require.ErrorIs(t, err, ErrUnknownHarness)
}

// --- claude-args ---------------------------------------------------------

func TestCLI_ClaudeArgsPassThrough(t *testing.T) {
	stdout, _, err := runCLI(t, underClaude, "--model", "size-md", "--dry-run",
		"--claude-args", `{"permission-mode": "plan"}`, "q")
	require.NoError(t, err)
	// The isolation --settings sits between the model and the caller's flag; it is
	// quoted by dry-run's printer because its JSON contains quotes.
	assert.Equal(t, "claude -p --model sonnet "+claudeSettingsArg+" --permission-mode plan -- q", strings.TrimSpace(stdout))
}

func TestCLI_MalformedClaudeArgsIsRefused(t *testing.T) {
	_, _, err := runCLI(t, underClaude, "--model", "size-md", "--dry-run",
		"--claude-args", "not json", "q")
	require.ErrorIs(t, err, ErrBadHarnessArgs)
}

// An explicitly EMPTY --claude-args is still a flag that was given, but it adds
// nothing, so it must not be an error.
func TestCLI_EmptyClaudeArgsIsAccepted(t *testing.T) {
	stdout, _, err := runCLI(t, underClaude, "--model", "size-md", "--dry-run",
		"--claude-args", "{}", "q")
	require.NoError(t, err)
	assert.Equal(t, "claude -p --model sonnet "+claudeSettingsArg+" -- q", strings.TrimSpace(stdout))
}

// --- exit codes ----------------------------------------------------------

// A refusal and a failure OF the run must be distinguishable, or a hook cannot
// tell a malformed command from an agent that ran and disagreed.
func TestExitCode_RefusalIsTwo(t *testing.T) {
	_, _, err := runCLI(t, underClaude, "--model", "gpt-5", "q")
	require.Error(t, err)
	assert.Equal(t, 2, exitCode(err))
}

func TestExitCode_HarnessFailurePassesItsOwnCodeThrough(t *testing.T) {
	err := &harnessRunError{binary: "claude", code: 7}
	assert.Equal(t, 7, exitCode(err))
	assert.Contains(t, err.Error(), "claude")
	assert.Contains(t, err.Error(), "7")
}

// --- help ----------------------------------------------------------------

// --help must document what an author needs to write a set correctly: every
// alias, and the two rules that make sets surprising.
func TestCLI_HelpDocumentsTheAliasesAndTheRules(t *testing.T) {
	cmd := newRoot()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--help"})
	require.NoError(t, cmd.Execute())

	help := out.String()
	for _, alias := range aliasNames() {
		assert.Contains(t, help, alias, "--help must document %s", alias)
	}
	assert.Contains(t, help, "first match wins")
	assert.Contains(t, help, "REFUSED")
	assert.Contains(t, help, "--harness")
	assert.Contains(t, help, "--claude-args")
}

// --- running the harness for real ----------------------------------------

// A missing binary must be explained in terms of the harness, not as a bare
// "file not found" — the caller's question is why the thing the environment
// says is running cannot be found.
func TestRunHarness_MissingBinaryIsExplained(t *testing.T) {
	cmd := &cobra.Command{}
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)

	err := runHarness(cmd, Invocation{
		Binary: "sr-agent-no-such-harness-binary", Args: []string{"-p"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sr-agent-no-such-harness-binary")
	assert.Contains(t, err.Error(), "PATH")
}

// A command that was never Execute()d has a nil context, and exec.CommandContext
// panics on one. Pinned because the panic would surface as a crash in whichever
// hook called this rather than as an error it could report.
func TestRunHarness_NilContextDoesNotPanic(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	cmd := &cobra.Command{}
	require.Nil(t, cmd.Context(), "this test is meaningless if cobra supplies a context here")

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))

	require.NotPanics(t, func() {
		require.NoError(t, runHarness(cmd, Invocation{Binary: "sh", Args: []string{"-c", "exit 0"}}))
	})
}

// The harness's exit code is passed through rather than flattened, and its
// streams are wired to the command's. Exercised against a real process.
func TestRunHarness_PassesThroughStreamsAndExitCode(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	cmd := &cobra.Command{}
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))

	err := runHarness(cmd, Invocation{
		Binary: "sh",
		Args:   []string{"-c", "echo the-answer; echo the-diagnostic >&2; exit 3"},
	})

	require.Error(t, err)
	assert.Equal(t, 3, exitCode(err), "the harness's own code must survive")
	assert.Equal(t, "the-answer\n", out.String(), "stdout must carry only what the harness wrote")
	assert.Equal(t, "the-diagnostic\n", errOut.String())
}

func TestRunHarness_SuccessIsSilent(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	cmd := &cobra.Command{}
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))

	require.NoError(t, runHarness(cmd, Invocation{Binary: "sh", Args: []string{"-c", "echo ok"}}))
	assert.Equal(t, "ok\n", out.String())
}

// --- sanitizing the child's environment -----------------------------------
//
// sr-agent's own most important caller is a guardrail firing from within an
// already-running Claude Code session, and Go's exec.Cmd inherits the FULL
// process environment whenever Env is left nil — which is what main.go used to
// do. That is a strong candidate for a nested one-shot `claude -p` judge call
// colliding with the STILL-LIVE parent session's own identity and IPC state.
// These tests pin sanitizeChildEnv's contract: strip exactly those four
// variables, keep everything else, including CLAUDECODE/CLAUDE_CODE_ENTRYPOINT
// which the nested claude still needs to know it is running headlessly.

// The falsifier for "the blocklist is actually applied": all four documented
// session-identity/IPC variables must be gone, together, in one pass — not
// just whichever one a narrower test happened to check.
// sr:proves judges/judge-agent-runs-isolated
func TestSanitizeChildEnv_StripsSessionIdentityAndIPCVars(t *testing.T) {
	in := []string{
		"CLAUDE_CODE_SESSION_ID=parent-session-abc",
		"CLAUDE_CODE_HOST_SESSION_ID=host-session-xyz",
		"CLAUDE_CODE_MESSAGING_SOCKET=/tmp/parent.sock",
		"CLAUDE_CODE_MESSAGING_TOKEN=super-secret-token",
		"PATH=/usr/bin:/bin",
	}
	out := sanitizeChildEnv(in)

	assert.Equal(t, []string{"PATH=/usr/bin:/bin"}, out)
}

// CLAUDECODE and CLAUDE_CODE_ENTRYPOINT are the two variables the nested
// `claude -p` actually needs to behave correctly as a one-shot, non-interactive
// call — the same pair claudeCodeSpec.detect reads. Stripping session identity
// must not take these with it.
// sr:proves judges/judge-agent-runs-isolated
func TestSanitizeChildEnv_KeepsEntrypointDetectionVars(t *testing.T) {
	in := []string{
		"CLAUDECODE=1",
		"CLAUDE_CODE_ENTRYPOINT=cli",
		"CLAUDE_CODE_SESSION_ID=parent-session-abc",
	}
	out := sanitizeChildEnv(in)

	assert.ElementsMatch(t, []string{"CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=cli"}, out)
}

// Ordinary environment a nested claude needs to run at all — auth and PATH,
// most of all — must survive untouched. This is the blocklist-not-allowlist
// property: sanitizeChildEnv has no idea what ANTHROPIC_API_KEY is for and
// must not need to.
// sr:proves judges/judge-agent-runs-isolated
func TestSanitizeChildEnv_KeepsUnrelatedVars(t *testing.T) {
	in := []string{
		"PATH=/usr/bin:/bin",
		"ANTHROPIC_API_KEY=sk-ant-example",
		"HOME=/home/x",
		"EDITOR=vim",
	}
	out := sanitizeChildEnv(in)

	assert.ElementsMatch(t, in, out, "nothing here is on the blocklist")
}

// A variable whose NAME merely contains one of the blocklisted names as a
// substring — rather than matching it exactly — must survive. The blocklist
// keys on the part before "=", not a substring search, so a hypothetical
// CLAUDE_CODE_SESSION_ID_BACKUP is not the same key as CLAUDE_CODE_SESSION_ID.
func TestSanitizeChildEnv_ExactKeyMatchOnly(t *testing.T) {
	in := []string{"CLAUDE_CODE_SESSION_ID_BACKUP=whatever"}
	out := sanitizeChildEnv(in)
	assert.Equal(t, in, out)
}

// A value that itself contains an "=" (a token, a JSON blob) must not confuse
// the key/value split — strings.Cut on the FIRST "=" is what keeps the key
// exactly "CLAUDE_CODE_MESSAGING_TOKEN" rather than something longer.
func TestSanitizeChildEnv_ValueContainingEqualsSign(t *testing.T) {
	in := []string{
		"CLAUDE_CODE_MESSAGING_TOKEN=abc=def=ghi",
		"SOME_CONFIG=key=value",
	}
	out := sanitizeChildEnv(in)
	assert.Equal(t, []string{"SOME_CONFIG=key=value"}, out)
}

// Empty input and input with none of the blocklisted vars are both handled —
// the falsifier for an implementation that assumes at least one hit.
func TestSanitizeChildEnv_NothingToStrip(t *testing.T) {
	assert.Empty(t, sanitizeChildEnv(nil))
	assert.Equal(t, []string{"PATH=/bin"}, sanitizeChildEnv([]string{"PATH=/bin"}))
}

// The end-to-end proof: a process actually run through runHarness must not see
// CLAUDE_CODE_SESSION_ID in its own environment, even though the test process
// running this assertion has it set — runHarness must be the thing that
// strips it, not merely a helper function nobody calls.
// sr:proves judges/judge-agent-runs-isolated
func TestRunHarness_ChildDoesNotInheritSessionIdentity(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "the-parent-session-must-not-leak")

	cmd := &cobra.Command{}
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))

	err := runHarness(cmd, Invocation{
		Binary: "sh",
		Args:   []string{"-c", `echo "[$CLAUDE_CODE_SESSION_ID]"`},
	})
	require.NoError(t, err)
	assert.Equal(t, "[]\n", out.String(), "the child must see an EMPTY value, not the parent's session id")
}
