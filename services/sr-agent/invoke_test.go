package main

import (
	"encoding/json"
	"github.com/sloprail/sloprail/internal/harness"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- harness args: real JSON, not a pattern match -------------------------

// The falsifier for the defect this repo has shipped before: a reader
// hard-coded to the literal `"key":"` works only on Go's own encoder output.
// Every one of these is valid JSON meaning the same thing, and all must parse.
func TestParseHarnessArgs_AcceptsAnyValidJSONSpacing(t *testing.T) {
	forms := []string{
		`{"permission-mode":"plan"}`,             // no spaces — what Go emits
		`{"permission-mode": "plan"}`,            // space after colon
		`{ "permission-mode" : "plan" }`,         // spaces everywhere
		"{\n  \"permission-mode\": \"plan\"\n}",  // pretty-printed, newlines
		"{\t\"permission-mode\"\t:\t\"plan\"\t}", // tabs
		`{"permission-mode"  :     "plan"    }`,  // ragged
	}
	for _, form := range forms {
		args, err := ParseHarnessArgs(form)
		require.NoError(t, err, "must parse %q", form)
		assert.Equal(t, []string{"--permission-mode", "plan"}, args, "for %q", form)
	}
}

// Escapes and non-ASCII must survive decoding, which a pattern match would
// mangle: `\u002d` is a hyphen and `\"` is a quote inside a value.
func TestParseHarnessArgs_DecodesEscapes(t *testing.T) {
	args, err := ParseHarnessArgs(`{"system\u002dprompt":"say \"hi\" — brièvement"}`)
	require.NoError(t, err)
	assert.Equal(t, []string{"--system-prompt", `say "hi" — brièvement`}, args)
}

// A value containing a comma, a brace or a colon is still one value. A
// hand-rolled splitter would break each of these.
func TestParseHarnessArgs_ValuesContainingJSONPunctuation(t *testing.T) {
	args, err := ParseHarnessArgs(`{"append-system-prompt":"a, b: {c} \"d\""}`)
	require.NoError(t, err)
	assert.Equal(t, []string{"--append-system-prompt", `a, b: {c} "d"`}, args)
}

// Numbers and booleans are valid JSON and obviously meant, so they are
// accepted and rendered as written.
func TestParseHarnessArgs_ScalarKinds(t *testing.T) {
	args, err := ParseHarnessArgs(`{"max-budget-usd":5,"verbose":true,"ratio":0.25,"neg":-3}`)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"--max-budget-usd", "5",
		"--neg", "-3",
		"--ratio", "0.25",
		"--verbose", "true",
	}, args)
}

// A big integer must not come back in scientific notation — the falsifier for
// decoding numbers into float64.
func TestParseHarnessArgs_LargeNumberKeepsItsLiteral(t *testing.T) {
	args, err := ParseHarnessArgs(`{"session-budget":10000000000000001}`)
	require.NoError(t, err)
	assert.Equal(t, []string{"--session-budget", "10000000000000001"}, args)
}

// One input must always produce one argv, or the command is unreproducible.
func TestParseHarnessArgs_KeysAreSorted(t *testing.T) {
	args, err := ParseHarnessArgs(`{"zebra":"1","alpha":"2","middle":"3"}`)
	require.NoError(t, err)
	assert.Equal(t, []string{"--alpha", "2", "--middle", "3", "--zebra", "1"}, args)
}

func TestParseHarnessArgs_EmptyIsNoArgs(t *testing.T) {
	for _, raw := range []string{"", "   ", "{}"} {
		args, err := ParseHarnessArgs(raw)
		require.NoError(t, err, "%q", raw)
		assert.Empty(t, args)
	}
}

func TestParseHarnessArgs_RejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"not JSON at all":  "permission-mode=plan",
		"an array":         `["a","b"]`,
		"a bare string":    `"plan"`,
		"a number":         `42`,
		"null":             `null`,
		"truncated":        `{"a":`,
		"nested object":    `{"a":{"b":"c"}}`,
		"nested array":     `{"a":["b"]}`,
		"null value":       `{"a":null}`,
		"empty key":        `{"":"x"}`,
		"two documents":    `{"a":"1"} {"b":"2"}`,
		"trailing garbage": `{"a":"1"} nonsense`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseHarnessArgs(raw)
			assert.ErrorIs(t, err, ErrBadHarnessArgs, "%q must be refused", raw)
		})
	}
}

// --- args for the wrong harness -------------------------------------------

func TestCheckHarnessArgs_MatchingHarnessIsFine(t *testing.T) {
	assert.NoError(t, CheckHarnessArgs("--claude-args", ClaudeCode, claudeCodeSpec))
}

// Reported, not dropped: the author expected settings that will not apply.
func TestCheckHarnessArgs_WrongHarnessIsReported(t *testing.T) {
	err := CheckHarnessArgs("--claude-args", ClaudeCode, fakeHarness("codex"))
	require.ErrorIs(t, err, ErrWrongHarnessArgs)
	assert.Contains(t, err.Error(), "--claude-args")
	assert.Contains(t, err.Error(), "claude-code")
	assert.Contains(t, err.Error(), "codex", "the running harness must be named too")
}

// --- invocation -----------------------------------------------------------

// noBinaryOverride is a getenv that names no environment at all — CLAUDE_CODE_EXECPATH
// unset, and neither of the detect variables set either. Every test in this section
// is about the ARGS BuildInvocation assembles, not which binary it picks, so they all
// go through the plain PATH-lookup path (resolveBinary's fallback) and keep asserting
// "claude" as they did before resolveBinary existed. resolveBinary itself gets its
// own dedicated tests below.
func noBinaryOverride(string) string { return "" }

// The prompt goes LAST and positionally. Last is what keeps a prompt beginning
// with a dash from being read as a flag.
func TestBuildInvocation_PromptIsLastAndPositional(t *testing.T) {
	inv := BuildInvocation(claudeCodeSpec, "sonnet", nil, "does this uphold the invariant?", noBinaryOverride)

	assert.Equal(t, "claude", inv.Binary)
	assert.Equal(t, []string{
		"-p", "--model", "sonnet",
		"--settings", claudeIsolationSettings, "--permission-mode", "default",
		"--", "does this uphold the invariant?",
	}, inv.Args)
	assert.Equal(t, "does this uphold the invariant?", inv.Args[len(inv.Args)-1])
}

// claudeIsolationSettings is the isolation --settings sr-agent always gives Claude
// Code (harnessSpec.baseArgs) — empty hooks/mcpServers/enabledPlugins so a launched
// judge carries none of the caller's session wiring and cannot recurse. Named here
// so the invocation tests assert against the same string the spec ships rather than
// re-spelling the JSON, and a change to the spec's settings updates one place.
const claudeIsolationSettings = harness.IsolationSettings

// claudeSettingsArg is how the isolation settings render in Invocation.String() /
// --dry-run output: the JSON contains quotes, so String() runs it through
// strconv.Quote. A dry-run string assertion inserts this after `--settings`.
var claudeSettingsArg = "--settings " + strconv.Quote(claudeIsolationSettings) + " --permission-mode default"

// The falsifier for the ordering claim: a prompt that looks exactly like a flag
// must still be the final argument, after everything else.
func TestBuildInvocation_DashLeadingPromptStaysLast(t *testing.T) {
	inv := BuildInvocation(claudeCodeSpec, "sonnet",
		[]string{"--permission-mode", "plan"}, "--model is not resolving, why?", noBinaryOverride)

	assert.Equal(t, []string{
		"-p", "--model", "sonnet",
		"--settings", claudeIsolationSettings, "--permission-mode", "default",
		"--permission-mode", "plan",
		"--", "--model is not resolving, why?",
	}, inv.Args)
	assert.Equal(t, "--model is not resolving, why?", inv.Args[len(inv.Args)-1])
}

// -p is always passed: a hook has no terminal, and an interactive session
// started there would hang holding the guardrail open.
func TestBuildInvocation_AlwaysNonInteractive(t *testing.T) {
	inv := BuildInvocation(claudeCodeSpec, "haiku", []string{"--verbose", "true"}, "q", noBinaryOverride)
	assert.Contains(t, inv.Args, "-p")
}

// Harness args are passed through untouched, between the model and the prompt.
func TestBuildInvocation_HarnessArgsPassThrough(t *testing.T) {
	inv := BuildInvocation(claudeCodeSpec, "opus",
		[]string{"--permission-mode", "plan", "--max-budget-usd", "5"}, "q", noBinaryOverride)

	assert.Equal(t, []string{
		"-p", "--model", "opus",
		"--settings", claudeIsolationSettings, "--permission-mode", "default",
		"--permission-mode", "plan", "--max-budget-usd", "5",
		"--", "q",
	}, inv.Args)
}

// BuildInvocation must not alias the caller's slice — a later append by the
// caller must not reach into an already-built invocation.
func TestBuildInvocation_DoesNotAliasHarnessArgs(t *testing.T) {
	harnessArgs := make([]string, 0, 8)
	harnessArgs = append(harnessArgs, "--verbose", "true")

	inv := BuildInvocation(claudeCodeSpec, "opus", harnessArgs, "q", noBinaryOverride)
	before := append([]string(nil), inv.Args...)

	harnessArgs = append(harnessArgs, "--sneaky", "value")
	_ = harnessArgs

	assert.Equal(t, before, inv.Args)
}

// A VARIADIC flag immediately before the prompt must not swallow it.
//
// This is a measured bug, not a hypothetical: claude's --add-dir takes
// `<directories...>`, and `claude -p --model haiku --add-dir /tmp/x "count the
// lines"` consumed the prompt as a second directory, then died with "Input must
// be provided either through stdin or as a prompt argument". The `--` separator
// is what makes the prompt a positional regardless of what precedes it.
func TestBuildInvocation_VariadicFlagCannotSwallowThePrompt(t *testing.T) {
	inv := BuildInvocation(claudeCodeSpec, "haiku",
		[]string{"--add-dir", "/tmp/out"}, "count the lines", noBinaryOverride)

	require.Equal(t, []string{
		"-p", "--model", "haiku",
		"--settings", claudeIsolationSettings, "--permission-mode", "default",
		"--add-dir", "/tmp/out",
		"--", "count the lines",
	}, inv.Args)

	// The separator must sit between the last flag and the prompt, or it
	// protects nothing.
	sep := indexOf(inv.Args, "--")
	require.NotEqual(t, -1, sep, "a -- separator must be present")
	assert.Equal(t, len(inv.Args)-2, sep, "-- must be immediately before the prompt")
}

func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

// The const the invocation assertions use must be the SAME settings the spec
// actually ships, or the tests would pass against a drifted baseArgs. This pins
// them together, so a change to claudeCodeSpec.baseArgs that forgot to update the
// const (or vice-versa) fails here rather than letting the two disagree.
// sr:proves judges/judge-agent-runs-isolated
func TestBaseArgs_IsolationSettingsMatchTheConst(t *testing.T) {
	assert.Equal(t, []string{"--settings", claudeIsolationSettings, "--permission-mode", "default"}, claudeCodeSpec.baseArgs)
}

// The isolation settings must disable hooks outright. Empty hooks/enabledPlugins
// objects do not: --settings is merged over the project and user settings, so a
// judge session would still run their Stop hooks and nest a judge inside a judge
// (measured against the real CLI; see claudeCodeSpec.baseArgs).
// sr:proves judges/judge-agent-runs-isolated
func TestBaseArgs_IsolationSettingsDisableAllHooks(t *testing.T) {
	var settings map[string]any
	require.NoError(t, json.Unmarshal([]byte(claudeCodeSpec.baseArgs[1]), &settings))
	assert.Equal(t, true, settings["disableAllHooks"], "a judge session must not run any hook")
}

// The isolation --settings is ALWAYS present, whatever else the caller passed —
// it is the whole point of moving it into baseArgs. Even a bare run with no caller
// args carries it.
// sr:proves judges/judge-agent-runs-isolated
func TestBuildInvocation_IsolationSettingsAlwaysPresent(t *testing.T) {
	inv := BuildInvocation(claudeCodeSpec, "haiku", nil, "q", noBinaryOverride)
	i := indexOf(inv.Args, "--settings")
	require.NotEqual(t, -1, i, "the isolation --settings must always be present")
	require.Less(t, i+1, len(inv.Args))
	assert.Equal(t, claudeIsolationSettings, inv.Args[i+1])
}

// ParseAllowedTools honours both separators claude documents and drops empties, so
// a stray comma grants nothing rather than a blank tool name.
func TestParseAllowedTools_SeparatorsAndEmpties(t *testing.T) {
	cases := map[string][]string{
		"Read WebFetch":      {"Read", "WebFetch"},
		"Read,WebFetch":      {"Read", "WebFetch"},
		"Read, WebFetch":     {"Read", "WebFetch"},
		" Read ,, WebFetch ": {"Read", "WebFetch"},
		"Read":               {"Read"},
	}
	for in, want := range cases {
		got, err := ParseAllowedTools(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, "input %q", in)
	}
	for _, empty := range []string{"", "   , ,  "} {
		got, err := ParseAllowedTools(empty)
		require.NoError(t, err)
		assert.Nil(t, got, "%q grants nothing", empty)
	}
}

// A scoped rule is ONE rule, whatever spaces or commas sit inside its
// parentheses: claude reads `Bash(git show:*)` as one rule, and splitting it
// into `Bash(git` + `show:*)` loses it once each piece is its own argv value.
func TestParseAllowedTools_ScopedRulesStayWhole(t *testing.T) {
	cases := map[string][]string{
		"Bash(git show:*)":                       {"Bash(git show:*)"},
		"Read Bash(curl -sL:*) WebFetch":         {"Read", "Bash(curl -sL:*)", "WebFetch"},
		"WebFetch(domain:code.claude.com),Read":  {"WebFetch(domain:code.claude.com)", "Read"},
		"Bash(printf a,b:*), Bash(git log -1:*)": {"Bash(printf a,b:*)", "Bash(git log -1:*)"},
		"Bash(echo (nested) x:*) Read":           {"Bash(echo (nested) x:*)", "Read"},
	}
	for in, want := range cases {
		got, err := ParseAllowedTools(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, "input %q", in)
	}
}

// Unbalanced parentheses are refused: there is no telling which rule was meant.
func TestParseAllowedTools_UnbalancedIsRefused(t *testing.T) {
	for _, bad := range []string{"Bash(git show:*", "Read) Write", "Bash((x:*)", "()", "(curl:*)", "Bash(x)y"} {
		_, err := ParseAllowedTools(bad)
		assert.ErrorIs(t, err, ErrBadAllowedTools, bad)
	}
}

// Through the CLI: a scoped rule with a space reaches the harness as ONE
// --allowed-tools value, beside the answer grant — not as two broken halves.
func TestCLI_ScopedToolRuleReachesTheHarnessWhole(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()
	script := writeScript(t, dir, "v.sh", "exit 0\n")
	t.Setenv(outputDirEnv, dir)

	stdout, _, err := runCLI(t, underClaude, "--model", "size-md", "--dry-run",
		"--allowed-tools", "Bash(git show:*) Bash(curl -sL:*) WebFetch(domain:code.claude.com)",
		"--verify", script, "q")
	require.NoError(t, err)
	assert.Contains(t, stdout, ` "Bash(git show:*)" "Bash(curl -sL:*)" "WebFetch(domain:code.claude.com)" -- `,
		"each scoped rule must be one argv value, in order, after the answer grant")
	assert.NotContains(t, stdout, `"Bash(git"`)
}

func TestInvocation_StringQuotesArgumentsWithSpaces(t *testing.T) {
	inv := BuildInvocation(claudeCodeSpec, "sonnet", nil, "does this hold?", noBinaryOverride)
	// The isolation --settings JSON contains quotes, so String() runs it through
	// strconv.Quote (claudeSettingsArg carries that quoted form); the prompt is the
	// argument with spaces and is quoted too.
	assert.Equal(t, `claude -p --model sonnet `+claudeSettingsArg+` -- "does this hold?"`, inv.String())
}

func TestInvocation_StringLeavesPlainArgumentsUnquoted(t *testing.T) {
	inv := BuildInvocation(claudeCodeSpec, "sonnet", nil, "why", noBinaryOverride)
	assert.Equal(t, "claude -p --model sonnet "+claudeSettingsArg+" -- why", inv.String())
}

// --- resolveBinary ----------------------------------------------------------
//
// sr-agent's own most important caller is a guardrail firing from WITHIN an
// already-running Claude Code session, which shells out to spawn a nested,
// one-shot `claude -p` judge call. Confirmed in a live session: the PARENT was
// running via CLAUDE_CODE_EXECPATH naming one build, while a bare "claude" on
// that same PATH resolved to a DIFFERENT, newer one — and the nested process
// crashed with a bare "claude exited with status 1". These tests pin the fix:
// prefer the parent's own binary when it is trustworthy, and never change
// behaviour when it is not.

// self is a file every one of these tests can stat as "exists and is not a
// directory" without depending on anything actually named claude being
// installed on the machine running the tests.
func selfExecutablePath(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	require.NoError(t, err, "need a real file to stand in for a resolved CLAUDE_CODE_EXECPATH")
	return path
}

// The confirmed-in-production case: both the Claude Code detect variables and
// CLAUDE_CODE_EXECPATH are set, and the path names a real file. This is what a
// nested sr-agent invocation sees, and it must get the parent's own binary
// rather than whatever PATH would resolve "claude" to.
func TestResolveBinary_PrefersExecPathWhenClaudeCodeDetectedAndFileExists(t *testing.T) {
	execPath := selfExecutablePath(t)
	getenv := envOf(map[string]string{
		"CLAUDECODE":           "1",
		"CLAUDE_CODE_EXECPATH": execPath,
	})
	assert.Equal(t, execPath, resolveBinary(claudeCodeSpec, getenv))
}

// The entrypoint variable alone is enough to detect Claude Code, matching
// claudeCodeSpec.detect's own "either alone is enough" rule.
func TestResolveBinary_EntrypointAloneIsEnoughToTrustExecPath(t *testing.T) {
	execPath := selfExecutablePath(t)
	getenv := envOf(map[string]string{
		"CLAUDE_CODE_ENTRYPOINT": "cli",
		"CLAUDE_CODE_EXECPATH":   execPath,
	})
	assert.Equal(t, execPath, resolveBinary(claudeCodeSpec, getenv))
}

// CLAUDE_CODE_EXECPATH unset falls back to the bare name — sr-agent invoked
// standalone, outside any live session, has no parent binary to prefer and
// this is the strict-improvement case: behaviour must be identical to before
// resolveBinary existed.
func TestResolveBinary_FallsBackWhenExecPathUnset(t *testing.T) {
	getenv := envOf(map[string]string{"CLAUDECODE": "1"})
	assert.Equal(t, "claude", resolveBinary(claudeCodeSpec, getenv))
}

// An empty CLAUDE_CODE_EXECPATH is the same as unset, per the same rule
// DetectHarness applies to CLAUDECODE itself: exported-but-blank is not a value.
func TestResolveBinary_EmptyExecPathFallsBack(t *testing.T) {
	getenv := envOf(map[string]string{
		"CLAUDECODE":           "1",
		"CLAUDE_CODE_EXECPATH": "",
	})
	assert.Equal(t, "claude", resolveBinary(claudeCodeSpec, getenv))
}

// CLAUDE_CODE_EXECPATH naming a path that does not exist must not be trusted —
// a stale or hand-edited value pointing nowhere is exactly the kind of thing
// that should fall back rather than hand exec.Command a path guaranteed to
// fail with "file not found" when the bare name might still resolve.
func TestResolveBinary_NonexistentExecPathFallsBack(t *testing.T) {
	getenv := envOf(map[string]string{
		"CLAUDECODE":           "1",
		"CLAUDE_CODE_EXECPATH": "/no/such/path/sr-agent-test-does-not-exist",
	})
	assert.Equal(t, "claude", resolveBinary(claudeCodeSpec, getenv))
}

// CLAUDE_CODE_EXECPATH naming a DIRECTORY, not a file, must not be trusted —
// exec.Command on a directory fails, and a directory is never what this
// variable is documented to hold, so this is treated the same as "does not
// point to a file that exists".
func TestResolveBinary_DirectoryExecPathFallsBack(t *testing.T) {
	getenv := envOf(map[string]string{
		"CLAUDECODE":           "1",
		"CLAUDE_CODE_EXECPATH": t.TempDir(),
	})
	assert.Equal(t, "claude", resolveBinary(claudeCodeSpec, getenv))
}

// The whole point of gating on detect: an environment that does NOT name a
// live Claude Code session must not have its CLAUDE_CODE_EXECPATH trusted,
// even if the variable happens to be set and to name a real file — e.g. a
// stale value left over in a shell's exported environment from an earlier
// session that is not the one running now. Without this gate, any process
// with that variable lingering in its environment would silently start
// execing a binary that has nothing to do with the current invocation.
func TestResolveBinary_ExecPathIgnoredWhenClaudeCodeNotDetected(t *testing.T) {
	execPath := selfExecutablePath(t)
	getenv := envOf(map[string]string{
		"CLAUDE_CODE_EXECPATH": execPath,
		// Neither CLAUDECODE nor CLAUDE_CODE_ENTRYPOINT set.
	})
	assert.Equal(t, "claude", resolveBinary(claudeCodeSpec, getenv))
}

// A nil getenv (BuildInvocation called without one) must behave exactly like
// today's bare spec.binary — the zero-value safe default for any caller this
// change did not anticipate.
func TestResolveBinary_NilGetenvFallsBack(t *testing.T) {
	assert.Equal(t, "claude", resolveBinary(claudeCodeSpec, nil))
}

// A harnessSpec with no detect (a bare struct literal, which is exactly how
// verify_test.go's fake harness is built) must fall back rather than panic —
// the falsifier for calling spec.detect unconditionally.
func TestResolveBinary_NilDetectFallsBackWithoutPanicking(t *testing.T) {
	spec := harnessSpec{name: "fake", binary: "fake-binary"}
	getenv := envOf(map[string]string{
		"CLAUDECODE":           "1",
		"CLAUDE_CODE_EXECPATH": selfExecutablePath(t),
	})
	require.NotPanics(t, func() {
		assert.Equal(t, "fake-binary", resolveBinary(spec, getenv))
	})
}

// --- --add-dir[:<mode>] ------------------------------------------------------

// Every path is made absolute (a permission rule matches absolute paths) and
// must be a directory — a typo would otherwise surface only as a judge denied
// every read. A path repeated in one mode is one grant; writable dirs come
// first, then readonly ones, each in the order its flags were given.
func TestResolveAddDirs(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	file := filepath.Join(dir, "f")
	require.NoError(t, os.WriteFile(file, nil, 0o644))

	got, err := resolveAddDirs(map[dirMode][]string{
		dirWritable: {other},
		dirReadonly: {dir, dir},
	})
	require.NoError(t, err)
	assert.Equal(t, []dirGrant{{Path: other, Mode: dirWritable}, {Path: dir, Mode: dirReadonly}}, got,
		"a repeated directory is one grant")

	t.Chdir(dir)
	got, err = resolveAddDirs(map[dirMode][]string{dirReadonly: {"."}})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, filepath.IsAbs(got[0].Path), "a relative dir is made absolute: %s", got[0].Path)

	for name, bad := range map[string]string{
		"missing": filepath.Join(dir, "nope"),
		"a file":  file,
		"empty":   "",
		"blank":   "  ",
	} {
		_, err := resolveAddDirs(map[dirMode][]string{dirReadonly: {bad}})
		assert.ErrorIs(t, err, ErrBadAddDir, name)
	}

	got, err = resolveAddDirs(nil)
	require.NoError(t, err)
	assert.Nil(t, got)
}

// One directory cannot be both writable and never writable.
func TestResolveAddDirs_OneDirInTwoModesIsRefused(t *testing.T) {
	dir := t.TempDir()
	_, err := resolveAddDirs(map[dirMode][]string{dirWritable: {dir}, dirReadonly: {dir}})
	require.ErrorIs(t, err, ErrBadAddDir)
	assert.Contains(t, err.Error(), "both writable and readonly")
}

// One directory spelled two ways (a symlink and its target, as /var and
// /private/var on macOS) is one directory: naming it writable one way and
// readonly the other is refused, not granted twice.
func TestResolveAddDirs_BothModesThroughASymlinkIsRefused(t *testing.T) {
	real := realDir(t, "real")
	link := filepath.Join(filepath.Dir(real), "link")
	require.NoError(t, os.Symlink(real, link))

	_, err := resolveAddDirs(map[dirMode][]string{dirWritable: {link}, dirReadonly: {real}})
	require.ErrorIs(t, err, ErrBadAddDir)
	assert.Contains(t, err.Error(), "both writable and readonly")
}

// A writable dir inside a readonly one is refused: the readonly deny covers it
// and no exception can be expressed, so the grant would silently do nothing.
// The reverse — readonly inside writable — is fine.
func TestResolveAddDirs_WritableInsideReadonlyIsRefused(t *testing.T) {
	project := realDir(t, "project")
	sub := filepath.Join(project, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	_, err := resolveAddDirs(map[dirMode][]string{dirWritable: {sub}, dirReadonly: {project}})
	require.ErrorIs(t, err, ErrBadAddDir)
	assert.Contains(t, err.Error(), "lies inside --add-dir:readonly")

	got, err := resolveAddDirs(map[dirMode][]string{dirWritable: {project}, dirReadonly: {sub}})
	require.NoError(t, err, "readonly inside writable is expressible: the deny wins")
	assert.Len(t, got, 2)
}

// A path with a glob character turns its rule into a pattern that misses the
// directory (measured in review: writes landed in a project named p[1]). It is
// refused, in either mode, rather than granted a rule that does not match.
func TestResolveAddDirs_GlobCharactersAreRefused(t *testing.T) {
	root := realDir(t, "root")
	for _, name := range []string{"p[1]", "p*", "p?", "p{a,b}", `p\x`} {
		dir := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(dir, 0o755), name)
		for _, mode := range []dirMode{dirWritable, dirReadonly} {
			_, err := resolveAddDirs(map[dirMode][]string{mode: {dir}})
			require.ErrorIs(t, err, ErrBadAddDir, "%s must be refused", name)
			assert.Contains(t, err.Error(), "glob character", name)
		}
	}
	plain := filepath.Join(root, "my proj (1)")
	require.NoError(t, os.MkdirAll(plain, 0o755))
	_, err := resolveAddDirs(map[dirMode][]string{dirReadonly: {plain}})
	assert.NoError(t, err, "spaces and parentheses are not glob characters")
}

// A harness that cannot express "read but never write" refuses a readonly dir
// rather than dropping it (a blind judge) or granting it plainly (a writable
// one). A writable dir costs it nothing — with no permission model everything
// is already writable — and the tools still pass through.
func TestHarnessGrant_NoPermissionModelRefusesReadonly(t *testing.T) {
	bare := harnessSpec{name: "bare", binary: "bare"}

	_, err := harnessGrant(bare, accessGrant{Dirs: []dirGrant{{Path: "/p", Mode: dirReadonly}}})
	require.ErrorIs(t, err, ErrModeUnsupported)
	assert.Contains(t, err.Error(), "--add-dir:readonly")

	got, err := harnessGrant(bare, accessGrant{
		Dirs:  []dirGrant{{Path: "/out", Mode: dirWritable}},
		Tools: []string{"Read", "WebFetch"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"--allowed-tools", "Read WebFetch"}, got)
}

// A harness with no permission model cannot deny a tool, so a deny list is
// refused rather than dropped — dropping it would run the agent with less
// confinement than the caller asked for.
func TestHarnessGrant_NoPermissionModelRefusesDenies(t *testing.T) {
	bare := harnessSpec{name: "bare", binary: "bare"}
	_, err := harnessGrant(bare, accessGrant{DenyTools: []string{"Bash(curl * -o *)"}})
	require.ErrorIs(t, err, ErrModeUnsupported)
	assert.Contains(t, err.Error(), "--disallowed-tools")
}

// Through the CLI: --disallowed-tools reaches the harness as whole rules, in the
// same --disallowed-tools group as the readonly project's deny, after the flag.
func TestCLI_DisallowedToolsReachTheHarnessWhole(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()
	script := writeScript(t, dir, "v.sh", "exit 0\n")
	t.Setenv(outputDirEnv, dir)
	project := filepath.Join(dir, "project")
	require.NoError(t, os.MkdirAll(project, 0o755))

	stdout, _, err := runCLI(t, underClaude, "--model", "size-md", "--dry-run",
		"--add-dir:readonly", project,
		"--allowed-tools", "Bash(curl:*)",
		"--disallowed-tools", "Bash(curl * -o *), Bash(curl * -d @*)",
		"--verify", script, "q")
	require.NoError(t, err)
	flags, _, _ := strings.Cut(stdout, " -- ")
	assert.Equal(t, 1, strings.Count(flags, "--disallowed-tools"), "one deny group")
	assert.Contains(t, flags, `--disallowed-tools "Edit(/`+project+`/**)"`)
	assert.True(t, strings.HasSuffix(flags, `"Bash(curl * -o *)" "Bash(curl * -d @*)"`),
		"the caller's denies follow the readonly deny as whole rules: %s", flags)
}

// An unbalanced deny rule is refused before anything runs.
func TestCLI_MalformedDisallowedToolsIsRefused(t *testing.T) {
	_, _, err := runCLI(t, underClaude, "--model", "size-md", "--dry-run",
		"--disallowed-tools", "Bash(curl * -o *", "q")
	require.ErrorIs(t, err, ErrBadAllowedTools)
	assert.Contains(t, err.Error(), "--disallowed-tools")
}

// `$` and backticks expand inside double quotes, so such an argument is
// single-quoted: pasted back into a shell, it is the same one argument.
func TestInvocationString_SingleQuotesExpansions(t *testing.T) {
	inv := Invocation{Binary: "claude", Args: []string{"--", "cost $HOME `id` it's"}}
	assert.Equal(t, `claude -- 'cost $HOME `+"`id`"+` it'\''s'`, inv.String())
}

// A permission rule's parentheses and `**` would break a pasted command.
func TestInvocationString_QuotesPermissionRules(t *testing.T) {
	inv := Invocation{Binary: "claude", Args: []string{"--allowed-tools", "Edit(//tmp/x/**)", "Read"}}
	assert.Equal(t, `claude --allowed-tools "Edit(//tmp/x/**)" Read`, inv.String())
}
