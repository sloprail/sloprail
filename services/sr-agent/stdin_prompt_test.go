package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A prompt past the harness's bound is not an argument: it rides on stdin, so an
// argv full of it cannot overflow ARG_MAX.
func TestBuildInvocation_HugePromptGoesOnStdin(t *testing.T) {
	huge := strings.Repeat("x", 2<<20)
	inv := BuildInvocation(claudeCodeSpec, "sonnet", nil, huge, func(string) string { return "" })

	assert.Equal(t, huge, inv.Stdin)
	for _, a := range inv.Args {
		assert.Less(t, len(a), 1<<10, "no argument may carry the prompt")
	}
	assert.NotContains(t, inv.Args, "--")
	assert.Contains(t, inv.String(), "on stdin")

	small := BuildInvocation(claudeCodeSpec, "sonnet", nil, "short", func(string) string { return "" })
	assert.Empty(t, small.Stdin, "a prompt under the bound stays positional")
	assert.Equal(t, "short", small.Args[len(small.Args)-1])
}

// runHarness feeds a >1 MB prompt to the harness whole, over stdin.
func TestRunHarness_FeedsAHugePromptOnStdin(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()
	got := filepath.Join(dir, "got")
	fake := writeScript(t, dir, "fake-claude.sh", "cat > '"+got+"'\n")

	huge := strings.Repeat("0123456789", 150_000) // 1.5 MB
	cmd := &cobra.Command{}
	cmd.SetIn(strings.NewReader("")) // the caller's stdin carries nothing
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	require.NoError(t, runHarness(cmd, Invocation{Binary: fake, Args: []string{"-p"}, Stdin: huge}))

	body, err := os.ReadFile(got)
	require.NoError(t, err)
	assert.Equal(t, huge, string(body))
}

// --prompt-stdin reads the prompt from stdin, and refuses a second prompt source.
func TestCLI_PromptStdin(t *testing.T) {
	t.Setenv("CLAUDE_CODE_EXECPATH", "")
	t.Setenv("CLAUDECODE", "")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "")
	run := func(args ...string) (string, error) {
		cmd := newRoot()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetIn(strings.NewReader("a prompt from a pipe"))
		cmd.SetArgs(args)
		err := cmd.Execute()
		return out.String(), err
	}
	out, err := run(underClaude, "--model", "size-md", "--dry-run", "--prompt-stdin")
	require.NoError(t, err)
	assert.Contains(t, out, `"a prompt from a pipe"`)

	_, err = run(underClaude, "--model", "size-md", "--dry-run", "--prompt-stdin", "also this")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--prompt-stdin")
}
