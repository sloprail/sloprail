package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexSpec_EverySizeAliasResolves(t *testing.T) {
	require.NoError(t, aliasesComplete())
}

func TestCodexSpec_IsDetectedFromWhatCodexSetsAndBeatsAnInheritedClaudeMarker(t *testing.T) {
	env := envOf(map[string]string{"CLAUDECODE": "1", "CODEX_THREAD_ID": "t"})
	spec, err := DetectHarness(env)
	require.NoError(t, err)
	assert.Equal(t, Codex, spec.name, "a Codex started inside a Claude Code shell is a Codex")

	spec, err = DetectHarness(envOf(map[string]string{"CLAUDECODE": "1"}))
	require.NoError(t, err)
	assert.Equal(t, ClaudeCode, spec.name)
}

func TestCodexSpec_BuildsAnExecInvocationThatLoadsNoHooks(t *testing.T) {
	inv := BuildInvocation(codexSpec, "gpt-6.1-sol", nil, "question", envOf(nil))
	assert.Equal(t, "codex", inv.Binary)
	assert.Equal(t, []string{"exec", "-m", "gpt-6.1-sol",
		"--ignore-user-config", "--disable", "hooks", "--ephemeral", "--skip-git-repo-check",
		"--", "question"}, inv.Args)
	assert.Empty(t, inv.Stdin)
}

func TestCodexSpec_ALargePromptGoesOnStdin(t *testing.T) {
	big := make([]byte, 70<<10)
	for i := range big {
		big[i] = 'x'
	}
	inv := BuildInvocation(codexSpec, "gpt-6.1-sol", nil, string(big), envOf(nil))
	assert.Equal(t, string(big), inv.Stdin)
	assert.Equal(t, "-", inv.Args[len(inv.Args)-1], "codex exec reads the prompt from stdin given -")
}

func TestCodexSpec_GrantIsTheSandbox(t *testing.T) {
	assert.Equal(t, []string{"--sandbox", "read-only"},
		codexSpec.grant(accessGrant{Dirs: []dirGrant{{Path: "/p", Mode: dirReadonly}}}),
		"a readonly directory needs nothing: read-only is the default for what is not added")
	assert.Equal(t, []string{"--sandbox", "workspace-write", "--add-dir", "/answer"},
		codexSpec.grant(accessGrant{Dirs: []dirGrant{{Path: "/p", Mode: dirReadonly}, {Path: "/answer", Mode: dirWritable}}}))
}

func TestClaudeCodeSpec_InvocationIsUnchangedByTheHarnessSeam(t *testing.T) {
	inv := BuildInvocation(claudeCodeSpec, "haiku", nil, "q", envOf(nil))
	assert.Equal(t, []string{"-p", "--model", "haiku"}, inv.Args[:3])
}
