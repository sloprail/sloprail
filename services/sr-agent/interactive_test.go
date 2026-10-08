package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The interactive agent under test is the harness's binary without its one-shot words and
// without a prompt, carrying the same unattended settings as the one-shot run.
func TestInteractive_InvocationHasNoOneShotWordsAndNoPrompt(t *testing.T) {
	stdout, _, err := runCLI(t, "--harness=cursor", "--model", "size-md", "--agent-run", "--interactive", "--dry-run")
	require.NoError(t, err)
	assert.Equal(t, "cursor-agent --model claude-sonnet-5-5-medium --trust --force", strings.TrimSpace(stdout))
}

// A judge keeps `-p`: only the agent under test has an interactive mode.
func TestInteractive_JudgeStaysOneShot(t *testing.T) {
	stdout, _, err := runCLI(t, "--harness=cursor", "--model", "size-md", "--dry-run", "judge this")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(strings.TrimSpace(stdout), "cursor-agent -p "), stdout)

	_, _, err = runCLI(t, "--harness=cursor", "--model", "size-md", "--interactive", "--dry-run")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--agent-run")
}

// The simulated user of a TUI run is granted `Bash(sr-eval-tui:*)` (services/sr-eval tuiUserGrant).
// Cursor names a shell command by its base only: that grant is expressible, the path-scoped
// one it replaced is refused.
func TestCursorToken_SimulatedUserGrant(t *testing.T) {
	got, err := cursorToken("Bash(sr-eval-tui:*)")
	require.NoError(t, err)
	assert.Equal(t, []string{"Shell(sr-eval-tui)"}, got)

	_, err = cursorToken("Bash(/var/x/sr-eval-tui tui:*)")
	require.Error(t, err)

	stdout, _, err := runCLI(t, "--harness=cursor", "--model", "size-sm", "--allowed-tools", "Bash(sr-eval-tui:*)", "--dry-run", "x")
	require.NoError(t, err)
	assert.Contains(t, stdout, "cursor-agent -p")
}

func TestInteractive_Refusals(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"a prompt is typed by the caller": {[]string{"--harness=cursor", "--agent-run", "--interactive", "--model", "size-md", "--prompt", "x"}, "takes no prompt"},
		"resume is for the one-shot run":  {[]string{"--harness=cursor", "--agent-run", "--interactive", "--model", "size-md", "--resume", "id"}, "--resume"},
		"a harness without a TUI mode":    {[]string{"--harness=claude", "--agent-run", "--interactive", "--model", "size-md"}, "no interactive mode"},
	} {
		_, _, err := runCLI(t, append(c.args, "--dry-run")...)
		if assert.Error(t, err, name) {
			assert.Contains(t, err.Error(), c.want, name)
		}
	}
}
