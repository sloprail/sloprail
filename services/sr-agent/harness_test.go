package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envOf builds a lookup over a fixed map. Detection takes a lookup rather than
// reading os.Getenv, so these tests never mutate the process environment —
// which under -race would put every test that reads a variable in a data race
// with every test that sets one.
func envOf(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

// --- detection succeeds ---------------------------------------------------

// Both variables were confirmed present in a live Claude Code session, and
// either alone is enough — a session setting only one is still recognised.
func TestDetectHarness_ClaudeCode(t *testing.T) {
	cases := []struct {
		name string
		vars map[string]string
	}{
		{"CLAUDECODE alone", map[string]string{"CLAUDECODE": "1"}},
		{"entrypoint alone", map[string]string{"CLAUDE_CODE_ENTRYPOINT": "cli"}},
		{"both", map[string]string{"CLAUDECODE": "1", "CLAUDE_CODE_ENTRYPOINT": "cli"}},
		{"alongside unrelated vars", map[string]string{"CLAUDECODE": "1", "PATH": "/usr/bin", "EDITOR": "vim"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := DetectHarness(envOf(tc.vars))
			require.NoError(t, err)
			assert.Equal(t, ClaudeCode, spec.name)
			assert.Equal(t, "claude", spec.binary)
		})
	}
}

// --- detection fails ------------------------------------------------------

// An unrecognised environment is NOT "probably Claude". The guess would be
// Claude Code, and being wrong means running a different agent than the rule
// asked for while reporting success.
func TestDetectHarness_UnknownEnvironmentIsRefused(t *testing.T) {
	cases := []struct {
		name string
		vars map[string]string
	}{
		{"empty environment", map[string]string{}},
		{"ordinary shell", map[string]string{"PATH": "/usr/bin", "HOME": "/home/x", "SHELL": "/bin/zsh"}},
		// The falsifier for a detector matching on a prefix rather than the
		// exact names: these are Claude-ISH but name no session.
		{"claude-ish but not the harness", map[string]string{
			"CLAUDE_CONFIG_DIR": "/home/x/.claude",
			"CLAUDE_API_KEY":    "sk-x",
			"CLAUDE":            "1",
		}},
		// A harness that exists but is not supported yet must fail detection,
		// not fall through to Claude Code.
		{"an unsupported harness", map[string]string{"CURSOR_AGENT": "1", "CODEX_SANDBOX": "1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DetectHarness(envOf(tc.vars))
			require.ErrorIs(t, err, ErrNoHarness)
			// A refusal must say what would have counted and how to proceed.
			assert.Contains(t, err.Error(), "claude-code")
			assert.Contains(t, err.Error(), "--harness")
		})
	}
}

// An empty variable is unset. An exported-but-blank CLAUDECODE is not a session.
func TestDetectHarness_EmptyValueDoesNotCount(t *testing.T) {
	_, err := DetectHarness(envOf(map[string]string{
		"CLAUDECODE": "", "CLAUDE_CODE_ENTRYPOINT": "",
	}))
	assert.ErrorIs(t, err, ErrNoHarness)
}

// --- --harness overrides --------------------------------------------------

// The override exists for a hook running outside any harness, where detection
// has nothing to read. So it must work on an environment where detection fails.
func TestResolveHarness_OverrideWinsWithNoEnvironmentAtAll(t *testing.T) {
	empty := envOf(map[string]string{})

	_, err := ResolveHarness("", empty)
	require.ErrorIs(t, err, ErrNoHarness, "detection must fail here, or the override proves nothing")

	spec, err := ResolveHarness("claude-code", empty)
	require.NoError(t, err)
	assert.Equal(t, ClaudeCode, spec.name)
}

// The override must beat a CONTRADICTING environment, not merely fill a gap.
// This is the falsifier for an implementation that consulted the environment
// first and used the override only as a fallback: here both are set and they
// disagree, and only one answer can be right.
func TestResolveHarness_OverrideBeatsAContradictingEnvironment(t *testing.T) {
	saved := harnesses
	// A second harness whose detector NEVER fires, so if the override is
	// honoured it can only be because the override was honoured.
	other := fakeHarness("codex")
	harnesses = []harnessSpec{claudeCodeSpec, other}
	t.Cleanup(func() { harnesses = saved })

	claudeEnv := envOf(map[string]string{"CLAUDECODE": "1"})

	detected, err := ResolveHarness("", claudeEnv)
	require.NoError(t, err)
	require.Equal(t, ClaudeCode, detected.name, "the environment must name Claude Code here")

	overridden, err := ResolveHarness("codex", claudeEnv)
	require.NoError(t, err)
	assert.Equal(t, Harness("codex"), overridden.name,
		"--harness must override detection, not defer to it")
}

// An override naming an unsupported harness is refused rather than falling
// through to detection. A caller who asked for Codex and silently got Claude
// Code is in exactly the position the refusal rule exists to prevent — and here
// detection WOULD have succeeded, which is what makes the fall-through
// tempting and the test meaningful.
func TestResolveHarness_UnsupportedOverrideIsRefusedEvenWhenDetectionWouldWork(t *testing.T) {
	claudeEnv := envOf(map[string]string{"CLAUDECODE": "1"})

	spec, err := ResolveHarness("codex", claudeEnv)
	require.ErrorIs(t, err, ErrUnknownHarness)
	assert.Empty(t, string(spec.name), "no harness may be chosen when the override is unsupported")
	assert.Contains(t, err.Error(), "codex", "the refusal must echo the name that was typed")
	assert.Contains(t, err.Error(), "claude-code", "and name what is supported")
}

func TestResolveHarness_OverrideIsCaseSensitiveAndExact(t *testing.T) {
	env := envOf(map[string]string{"CLAUDECODE": "1"})
	for _, name := range []string{"Claude-Code", "CLAUDE-CODE", "claude", "claudecode", " claude-code"} {
		_, err := ResolveHarness(name, env)
		assert.ErrorIs(t, err, ErrUnknownHarness, "%q must not be accepted as a harness name", name)
	}
}

func TestSupportedNames_ListsTheRegistry(t *testing.T) {
	assert.Equal(t, []string{"claude-code"}, supportedNames())
}

func TestLookupSpec(t *testing.T) {
	spec, ok := lookupSpec(ClaudeCode)
	require.True(t, ok)
	assert.Equal(t, "claude", spec.binary)

	_, ok = lookupSpec("nope")
	assert.False(t, ok)
}

// Every registered harness must be internally coherent: a name, a binary, a
// detector, an offers test, and a complete size table.
func TestRegistry_EveryHarnessIsWellFormed(t *testing.T) {
	require.NotEmpty(t, harnesses)
	for _, spec := range harnesses {
		assert.NotEmpty(t, string(spec.name))
		assert.NotEmpty(t, spec.binary)
		assert.NotNil(t, spec.detect, "%s needs a detector", spec.name)
		assert.NotNil(t, spec.offers, "%s needs an offers test", spec.name)
		assert.NotEmpty(t, spec.argsFlag, "%s needs an args flag", spec.name)
		assert.Len(t, spec.sizes, len(sizeAliases), "%s must map every size", spec.name)
	}
}
