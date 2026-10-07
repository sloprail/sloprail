package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
		{"an unsupported harness", map[string]string{"CODEX_SANDBOX": "1"}},
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
	assert.Equal(t, []string{"claude-code", "cursor"}, supportedNames())
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

// --- the file-access grant ------------------------------------------------

// realDir is a fresh directory whose path has no symlink in it, so pathRules
// yields exactly one spelling and a test can compare argv exactly. (t.TempDir on
// macOS sits under /var, a symlink to /private/var.)
func realDir(t *testing.T, name string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	return dir
}

func writable(p string) dirGrant { return dirGrant{Path: p, Mode: dirWritable} }
func readonly(p string) dirGrant { return dirGrant{Path: p, Mode: dirReadonly} }

// A writable dir is a working directory plus an Edit rule scoped to it — never
// an unscoped Write, which was measured to write anywhere on disk. The answer
// folder of a --verify run is just such a dir. The caller's own tools follow as
// further values of the same flag.
func TestClaudeGrant_WritableDirIsAddedAndAllowed(t *testing.T) {
	answer := realDir(t, "answer")

	got := claudeCodeSpec.grant(accessGrant{Dirs: []dirGrant{writable(answer)}, Tools: []string{"Read", "WebFetch"}})

	assert.Equal(t, []string{
		"--add-dir", answer,
		"--allowed-tools", "Edit(/" + answer + "/**)", "Read", "WebFetch",
	}, got)
	assert.NotContains(t, got, "Write", "an unscoped Write grant writes anywhere on disk")
}

// A readonly dir is a working directory (readable by Read/Grep/Glob with no
// grant) and denied to every file-writing tool — a deny beats any allow,
// including a Write the caller itself asked for.
// sr:proves judges/judge-cannot-change-the-project
func TestClaudeGrant_ReadonlyDirIsAddedAndDenied(t *testing.T) {
	project := realDir(t, "project")

	got := claudeCodeSpec.grant(accessGrant{Dirs: []dirGrant{readonly(project)}, Tools: []string{"Write"}})

	assert.Equal(t, []string{
		"--add-dir", project,
		"--allowed-tools", "Write",
		"--disallowed-tools", "Edit(/" + project + "/**)",
	}, got)
}

// Mixed modes land in ONE --add-dir (in the order given), one --allowed-tools
// and one --disallowed-tools: never repeated variadic groups.
func TestClaudeGrant_MixedModesShareOneFlagEach(t *testing.T) {
	project := realDir(t, "project")
	scratch := realDir(t, "scratch")
	answer := realDir(t, "answer")

	got := claudeCodeSpec.grant(accessGrant{
		Dirs:  []dirGrant{readonly(project), writable(scratch), writable(answer)},
		Tools: []string{"WebFetch"},
	})

	assert.Equal(t, []string{
		"--add-dir", project, scratch, answer,
		"--allowed-tools", "Edit(/" + scratch + "/**)", "Edit(/" + answer + "/**)", "WebFetch",
		"--disallowed-tools", "Edit(/" + project + "/**)",
	}, got)
	assert.Empty(t, claudeCodeSpec.grant(accessGrant{}), "no access asked for, no flags")
}

// The caller's own deny rules join the readonly-dir denies in ONE
// --disallowed-tools group, each rule whole.
func TestClaudeGrant_CallerDeniesJoinTheReadonlyDenies(t *testing.T) {
	project := realDir(t, "project")

	got := claudeCodeSpec.grant(accessGrant{
		Dirs:      []dirGrant{readonly(project)},
		Tools:     []string{"Bash(curl:*)"},
		DenyTools: []string{"Bash(curl * -o *)", "Bash(curl * -d @*)"},
	})

	assert.Equal(t, []string{
		"--add-dir", project,
		"--allowed-tools", "Bash(curl:*)",
		"--disallowed-tools", "Edit(/" + project + "/**)", "Bash(curl * -o *)", "Bash(curl * -d @*)",
	}, got)

	onlyDenies := claudeCodeSpec.grant(accessGrant{DenyTools: []string{"WebSearch"}})
	assert.Equal(t, []string{"--disallowed-tools", "WebSearch"}, onlyDenies)
}

// A readonly dir is denied ALWAYS — even when a writable dir sits inside it.
// The old exception dropped the deny there, and a judge with Write then wrote
// into the project (measured in review). claude cannot express "deny except
// this sub-dir", so resolveAddDirs and runVerified keep writable dirs out of
// readonly ones instead; the grant never weakens the deny.
// sr:proves judges/judge-cannot-change-the-project
func TestClaudeGrant_ReadonlyDenyIsNeverDropped(t *testing.T) {
	project := realDir(t, "project")
	inner := filepath.Join(project, "tmp", "answer")
	require.NoError(t, os.MkdirAll(inner, 0o755))

	got := claudeCodeSpec.grant(accessGrant{Dirs: []dirGrant{readonly(project), writable(inner)}})

	i := indexOf(got, "--disallowed-tools")
	require.NotEqual(t, -1, i, "the readonly project must be denied: %v", got)
	assert.Contains(t, got[i+1:], "Edit(/"+project+"/**)")
}

// A readonly dir NESTED in a writable one stays readonly: its deny is emitted,
// and a deny beats the enclosing allow.
func TestClaudeGrant_ReadonlyNestedInWritableStaysReadonly(t *testing.T) {
	scratch := realDir(t, "scratch")
	vendored := filepath.Join(scratch, "vendor")
	require.NoError(t, os.MkdirAll(vendored, 0o755))

	got := claudeCodeSpec.grant(accessGrant{Dirs: []dirGrant{writable(scratch), readonly(vendored)}})

	assert.Equal(t, []string{
		"--add-dir", scratch, vendored,
		"--allowed-tools", "Edit(/" + scratch + "/**)",
		"--disallowed-tools", "Edit(/" + vendored + "/**)",
	}, got)
}

// claude matches a rule against the path as the agent SPELLS it (measured: an
// allow for the /private/var spelling did not cover a Write to the /var one), so
// a directory reached through a symlink gets a rule for each spelling.
func TestPathRules_EverySpellingOfASymlinkedDirectory(t *testing.T) {
	real := realDir(t, "real")
	link := filepath.Join(filepath.Dir(real), "link")
	require.NoError(t, os.Symlink(real, link))

	assert.Equal(t, []string{"Edit(/" + link + "/**)", "Edit(/" + real + "/**)"}, pathRules("Edit", link))
	assert.Equal(t, []string{"Edit(/" + real + "/**)"}, pathRules("Edit", real),
		"a path with no symlink in it has one spelling")
}

func TestWithin(t *testing.T) {
	root := realDir(t, "root")
	assert.True(t, within(root, root))
	assert.True(t, within(filepath.Join(root, "a", "b"), root))
	assert.False(t, within(filepath.Dir(root), root))
	assert.False(t, within(root+"-sibling", root), "a shared prefix is not containment")
}

// Cursor sets CURSOR_AGENT=1 and CURSOR_INVOKED_AS in the environment of every
// command the agent runs (harness-mocks cursor-mock/snapshots/runs/subprocess-session-env),
// and CLAUDE_PROJECT_DIR too, which must not read as Claude Code.
func TestDetectHarness_Cursor(t *testing.T) {
	for _, vars := range []map[string]string{
		{"CURSOR_AGENT": "1"},
		{"CURSOR_INVOKED_AS": "cursor-agent"},
		{"CURSOR_AGENT": "1", "CLAUDE_PROJECT_DIR": "/p"},
	} {
		spec, err := DetectHarness(envOf(vars))
		require.NoError(t, err)
		assert.Equal(t, Cursor, spec.name)
		assert.Equal(t, "cursor-agent", spec.binary)
	}
}

func TestCursorSpec_Invocation(t *testing.T) {
	require.NoError(t, aliasesComplete())
	inv := BuildInvocation(cursorSpec, "auto", nil, "hello", func(string) string { return "" })
	assert.Equal(t, []string{"-p", "--model", "auto", "--trust", "--", "hello"}, inv.Args)
	assert.True(t, cursorSpec.offers("auto"))
	assert.True(t, cursorSpec.offers("cursor-grok-4.5-high"))
	assert.True(t, cursorSpec.offers("claude-sonnet-5-5-medium"))
	assert.False(t, cursorSpec.offers("haiku"), "a Claude Code family alias is not a Cursor model")

	big := strings.Repeat("x", cursorSpec.stdinPromptAbove+1)
	inv = BuildInvocation(cursorSpec, "auto", nil, big, func(string) string { return "" })
	assert.Equal(t, big, inv.Stdin, "a large prompt goes on stdin (measured: cursor-agent -p reads it there)")
	assert.NotContains(t, inv.Args, big)

	// A readonly dir is a deny in the run's private config, not a refusal.
	g := accessGrant{Dirs: []dirGrant{{Path: "/p", Mode: dirReadonly}, {Path: "/w", Mode: dirWritable}}}
	args, err := harnessGrant(cursorSpec, g)
	require.NoError(t, err)
	assert.Empty(t, args)
	env, _, cleanup, err := harnessGrantEnv(cursorSpec, g)
	require.NoError(t, err)
	defer cleanup()
	require.Len(t, env, 2)
	assert.Equal(t, `SLOPRAIL_JUDGE_GRANT={"writable":["/w"],"readonly":["/p"]}`, env[1])
	dir := strings.TrimPrefix(env[0], "CURSOR_CONFIG_DIR=")
	raw, err := os.ReadFile(filepath.Join(dir, "cli-config.json"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"permissions":{"allow":[],"deny":["Write(/p/**)"]}}`, string(raw))
	cleanup()
	_, statErr := os.Stat(dir)
	assert.True(t, os.IsNotExist(statErr), "the private config dir is removed")
}

func TestCursorRules_ToolMapping(t *testing.T) {
	allow, deny, err := cursorRules(accessGrant{Tools: []string{"Read", "WebFetch", "Bash(curl:*)", "Edit(//abs/x/**)"}, DenyTools: []string{"Write(//abs/y/**)"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"Read(**)", "WebFetch(*)", "Shell(curl)", "Write(/abs/x/**)"}, allow)
	assert.Equal(t, []string{"Write(/abs/y/**)"}, deny)

	_, _, err = cursorRules(accessGrant{Tools: []string{"Bash"}, DenyTools: []string{"Bash(rm:*)"}})
	assert.ErrorIs(t, err, ErrModeUnsupported, "a shell deny beside a shell grant was not enforced by cursor-agent (measured)")
}

func TestCursorSizesAreCatalogueModels(t *testing.T) {
	for alias, model := range cursorSpec.sizes {
		assert.True(t, cursorSpec.offers(model), "%s -> %s", alias, model)
	}
	assert.NotContains(t, cursorSpec.sizes[SizeXXL], "fable", "Fable is listed NO ZDR in cursor-agent --list-models")
}

func TestSanitizeChildEnvStripsCursorSessionIdentity(t *testing.T) {
	t.Setenv("SLOPRAIL_HARNESS", "cursor") // the strip is the running harness's (ChildEnvBlocklist)
	out := sanitizeChildEnv([]string{"CURSOR_CONVERSATION_ID=c", "CURSOR_REQUEST_ID=r", "CURSOR_TRANSCRIPT_PATH=/t", "CURSOR_AGENT=1", "CURSOR_API_KEY=k"})
	assert.Equal(t, []string{"CURSOR_AGENT=1", "CURSOR_API_KEY=k"}, out)
}

// A plugin's hook wrapper names its harness with SLOPRAIL_HARNESS and sets none of the
// markers the harness puts on its own shell commands, so a judge launched from it must
// resolve the harness from that name alone.
func TestDetectHarness_NamedBySloprailHarness(t *testing.T) {
	for name, want := range map[string]Harness{
		"cursor":      Cursor,
		"claude-code": ClaudeCode,
	} {
		spec, err := DetectHarness(envOf(map[string]string{"SLOPRAIL_HARNESS": name}))
		if err != nil || spec.name != want {
			t.Errorf("SLOPRAIL_HARNESS=%s: got %q, %v; want %q", name, spec.name, err, want)
		}
	}
	if _, err := DetectHarness(envOf(map[string]string{"SLOPRAIL_HARNESS": "nope"})); !errors.Is(err, ErrUnknownHarness) {
		t.Errorf("an unknown SLOPRAIL_HARNESS must be refused, got %v", err)
	}
}
