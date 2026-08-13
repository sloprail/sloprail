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
func runCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
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
const underClaude = "--harness=claude-code"

// --- prompt --------------------------------------------------------------

func TestCLI_PositionalPrompt(t *testing.T) {
	stdout, _, err := runCLI(t, underClaude, "--model", "size-md", "--dry-run", "is this right?")
	require.NoError(t, err)
	assert.Equal(t, `claude -p --model sonnet "is this right?"`, strings.TrimSpace(stdout))
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
	assert.NotContains(t, stderr, "harness claude-code, model",
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
	assert.Equal(t, "claude -p --model sonnet q", strings.TrimSpace(stdout))
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
	_, _, err := runCLI(t, "--harness", "codex", "--model", "size-md", "--dry-run", "q")
	require.ErrorIs(t, err, ErrUnknownHarness)
}

// --- claude-args ---------------------------------------------------------

func TestCLI_ClaudeArgsPassThrough(t *testing.T) {
	stdout, _, err := runCLI(t, underClaude, "--model", "size-md", "--dry-run",
		"--claude-args", `{"permission-mode": "plan"}`, "q")
	require.NoError(t, err)
	assert.Equal(t, "claude -p --model sonnet --permission-mode plan q", strings.TrimSpace(stdout))
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
	assert.Equal(t, "claude -p --model sonnet q", strings.TrimSpace(stdout))
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
