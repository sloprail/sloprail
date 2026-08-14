package main

import (
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

// The prompt goes LAST and positionally. Last is what keeps a prompt beginning
// with a dash from being read as a flag.
func TestBuildInvocation_PromptIsLastAndPositional(t *testing.T) {
	inv := BuildInvocation(claudeCodeSpec, "sonnet", nil, "does this uphold the invariant?")

	assert.Equal(t, "claude", inv.Binary)
	assert.Equal(t, []string{"-p", "--model", "sonnet", "--", "does this uphold the invariant?"}, inv.Args)
	assert.Equal(t, "does this uphold the invariant?", inv.Args[len(inv.Args)-1])
}

// The falsifier for the ordering claim: a prompt that looks exactly like a flag
// must still be the final argument, after everything else.
func TestBuildInvocation_DashLeadingPromptStaysLast(t *testing.T) {
	inv := BuildInvocation(claudeCodeSpec, "sonnet",
		[]string{"--permission-mode", "plan"}, "--model is not resolving, why?")

	assert.Equal(t, []string{
		"-p", "--model", "sonnet",
		"--permission-mode", "plan",
		"--", "--model is not resolving, why?",
	}, inv.Args)
	assert.Equal(t, "--model is not resolving, why?", inv.Args[len(inv.Args)-1])
}

// -p is always passed: a hook has no terminal, and an interactive session
// started there would hang holding the guardrail open.
func TestBuildInvocation_AlwaysNonInteractive(t *testing.T) {
	inv := BuildInvocation(claudeCodeSpec, "haiku", []string{"--verbose", "true"}, "q")
	assert.Contains(t, inv.Args, "-p")
}

// Harness args are passed through untouched, between the model and the prompt.
func TestBuildInvocation_HarnessArgsPassThrough(t *testing.T) {
	inv := BuildInvocation(claudeCodeSpec, "opus",
		[]string{"--permission-mode", "plan", "--max-budget-usd", "5"}, "q")

	assert.Equal(t, []string{
		"-p", "--model", "opus",
		"--permission-mode", "plan", "--max-budget-usd", "5",
		"--", "q",
	}, inv.Args)
}

// BuildInvocation must not alias the caller's slice — a later append by the
// caller must not reach into an already-built invocation.
func TestBuildInvocation_DoesNotAliasHarnessArgs(t *testing.T) {
	harnessArgs := make([]string, 0, 8)
	harnessArgs = append(harnessArgs, "--verbose", "true")

	inv := BuildInvocation(claudeCodeSpec, "opus", harnessArgs, "q")
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
		[]string{"--add-dir", "/tmp/out"}, "count the lines")

	require.Equal(t, []string{
		"-p", "--model", "haiku",
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

func TestInvocation_StringQuotesArgumentsWithSpaces(t *testing.T) {
	inv := BuildInvocation(claudeCodeSpec, "sonnet", nil, "does this hold?")
	assert.Equal(t, `claude -p --model sonnet -- "does this hold?"`, inv.String())
}

func TestInvocation_StringLeavesPlainArgumentsUnquoted(t *testing.T) {
	inv := BuildInvocation(claudeCodeSpec, "sonnet", nil, "why")
	assert.Equal(t, "claude -p --model sonnet -- why", inv.String())
}
