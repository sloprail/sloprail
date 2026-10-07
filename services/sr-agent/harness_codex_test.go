package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexSpec_EverySizeAliasResolves(t *testing.T) {
	require.NoError(t, aliasesComplete())
}

func TestCodexSpec_IsDetectedFromWhatCodexSetsAndYieldsToClaudeMarkers(t *testing.T) {
	env := envOf(map[string]string{"CLAUDECODE": "1", "CODEX_THREAD_ID": "t"})
	spec, err := DetectHarness(env)
	require.NoError(t, err)
	assert.Equal(t, ClaudeCode, spec.name, "both sets of markers: ambiguous, Claude Code wins (a hook names its harness explicitly)")

	spec, err = DetectHarness(envOf(map[string]string{"CODEX_THREAD_ID": "t"}))
	require.NoError(t, err)
	assert.Equal(t, Codex, spec.name)

	spec, err = DetectHarness(envOf(map[string]string{"CLAUDECODE": "1", "SLOPRAIL_HARNESS": "codex"}))
	require.NoError(t, err)
	assert.Equal(t, Codex, spec.name, "an explicit name beats every marker")

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
	assert.Equal(t, []string{
		"-C", "/answer", "--sandbox", "workspace-write",
		"-c", "sandbox_workspace_write.exclude_slash_tmp=true",
		"-c", "sandbox_workspace_write.exclude_tmpdir_env_var=true",
	}, codexSpec.grant(accessGrant{Dirs: []dirGrant{{Path: "/p", Mode: dirReadonly}, {Path: "/answer", Mode: dirWritable}}}),
		"the answer folder is the working directory and the only writable root; /tmp is not")
	assert.Equal(t, []string{"--add-dir", "/b"},
		codexSpec.grant(accessGrant{Dirs: []dirGrant{{Path: "/a", Mode: dirWritable}, {Path: "/b", Mode: dirWritable}}})[8:],
		"further writable directories ride on --add-dir")
}

func TestCodexSpec_AReadonlyDirInsideAWritableOneIsRefused(t *testing.T) {
	_, err := harnessGrant(codexSpec, accessGrant{Dirs: []dirGrant{{Path: "/w", Mode: dirWritable}, {Path: "/w/ro", Mode: dirReadonly}}})
	assert.ErrorIs(t, err, ErrModeUnsupported)
}

func TestCodexSpec_ToolsMapToTheSandboxAndAreRefusedWhenTheyCannotBe(t *testing.T) {
	answer := dirGrant{Path: "/answer", Mode: dirWritable}
	got, err := harnessGrant(codexSpec, accessGrant{Dirs: []dirGrant{answer}, Tools: []string{"Read", "Write", "WebSearch"}})
	require.NoError(t, err)
	assert.Contains(t, got, `web_search="live"`)
	assert.Contains(t, got, "sandbox_workspace_write.network_access=false")
	assert.Equal(t, []string{"--disable", "multi_agent"}, got[len(got)-2:], "sub-agents are off unless allowed")

	got, err = harnessGrant(codexSpec, accessGrant{Dirs: []dirGrant{answer}, Tools: []string{"WebFetch"}})
	require.NoError(t, err)
	assert.Contains(t, got, "sandbox_workspace_write.network_access=true")
	assert.Contains(t, got, `web_search="disabled"`)

	for _, tc := range []accessGrant{
		{Dirs: []dirGrant{answer}, Tools: []string{"Bash(ls:*)"}},
		{Dirs: []dirGrant{answer}, Tools: []string{"Edit(//x/**)"}},
		{Dirs: []dirGrant{answer}, Tools: []string{"mcp__s__t"}},
		{Tools: []string{"Write"}},
		{Tools: []string{"WebFetch"}},
		{Dirs: []dirGrant{answer}, DenyTools: []string{"Bash"}},
		{Dirs: []dirGrant{answer}, Tools: []string{"WebSearch"}, DenyTools: []string{"WebSearch"}},
	} {
		_, err := harnessGrant(codexSpec, tc)
		assert.ErrorIs(t, err, ErrModeUnsupported, "%+v", tc)
	}
}

func TestClaudeCodeSpec_InvocationIsUnchangedByTheHarnessSeam(t *testing.T) {
	inv := BuildInvocation(claudeCodeSpec, "haiku", nil, "q", envOf(nil))
	assert.Equal(t, []string{"-p", "--model", "haiku"}, inv.Args[:3])
}
