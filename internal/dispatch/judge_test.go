package dispatch

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The judge's verify script is what constrains a model's answer to a pass/fail
// verdict, and its exit code IS the verdict sr-agent reports. These run the real
// script against verdict files, pinning the two subtle points the whole judge path
// rests on: a `false` verdict must be read as a clean fail (not "not a boolean"),
// and a passing verdict must exit 0. The jq trap here — `.pass // empty` turning a
// real `false` into empty — is the exact class of bug this codebase has been burned
// by, so it is locked with a test rather than left to the e2e alone.

// runVerifier stages the real verifier script and runs it against a verdict file.
func runVerifier(t *testing.T, verdict string) (code int, stderr string) {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH — the verifier script needs it")
	}
	dir := t.TempDir()
	vf := filepath.Join(dir, "verify.sh")
	require.NoError(t, os.WriteFile(vf, []byte(verifierScript), 0o755))
	out := filepath.Join(dir, "answer")
	require.NoError(t, os.WriteFile(out, []byte(verdict), 0o644))

	cmd := exec.Command(vf, out)
	cmd.Stdin = strings.NewReader(verdict)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code = 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("verifier run: %v", err)
	}
	return code, errBuf.String()
}

// A passing verdict exits 0.
func TestVerifier_PassExitsZero(t *testing.T) {
	code, _ := runVerifier(t, `{"pass": true, "reasoning": ""}`)
	assert.Equal(t, 0, code, "a passing verdict must exit 0")
}

// A FALSE verdict is a clean fail — exit non-zero, reasoning surfaced — NOT "not a
// boolean". This is the jq `// empty` trap: `.pass // empty` reads a boolean false
// as empty, which would misreport a real refusal as a malformed verdict.
func TestVerifier_FalseIsCleanFail(t *testing.T) {
	code, stderr := runVerifier(t, `{"pass": false, "reasoning": "the proof is missing"}`)
	assert.NotEqual(t, 0, code, "a failing verdict must exit non-zero")
	assert.Contains(t, stderr, "JUDGE-REASON-JSON:", "the reasoning is surfaced for the engine to recover")
	assert.Contains(t, stderr, "the proof is missing")
	assert.NotContains(t, stderr, "not a boolean", "a real false must not be misread as a malformed verdict")
}

// A clean fail exits 3 — sr-agent's final rejection — so the judge is not
// re-asked for a verdict that was already well formed.
func TestVerifier_CleanFailIsFinal(t *testing.T) {
	code, _ := runVerifier(t, `{"pass": false, "reasoning": "the proof is missing"}`)
	assert.Equal(t, 3, code, "a well-formed fail must be final (exit 3), not a re-ask")
}

// A failing verdict whose reasoning quotes code with braces is still read as
// that verdict. The flat-object pattern used to grab the quoted fragment
// (`{kind, fqn, line}`), misread every such refusal as "not a boolean", and
// re-ask the judge — measured on every refusal of a real onboarding run.
func TestVerifier_BracesInReasoningAreNotTheVerdict(t *testing.T) {
	verdict := `{"pass": false, "reasoning": "markers are {kind, fqn, line} objects; use ${payload} and .event.newContent"}`
	code, stderr := runVerifier(t, verdict)
	assert.Equal(t, 3, code)
	assert.Contains(t, stderr, "markers are {kind, fqn, line} objects")
	assert.NotContains(t, stderr, "not a boolean")
}

// A verdict whose `pass` is neither true nor false is malformed — the script asks
// for a retry rather than guessing.
func TestVerifier_NonBooleanAsksRetry(t *testing.T) {
	code, stderr := runVerifier(t, `{"pass": "maybe", "reasoning": "x"}`)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "not a boolean")
}

// No JSON object at all is a retry, not a pass — an empty or prose-only answer must
// not slip through as approval.
func TestVerifier_NoVerdictAsksRetry(t *testing.T) {
	code, stderr := runVerifier(t, `I could not decide.`)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "did not produce a JSON verdict")
}

// A verdict wrapped in a ```json code fence is still read — models fence their
// output, and the script strips the fence before extracting.
func TestVerifier_StripsCodeFence(t *testing.T) {
	code, _ := runVerifier(t, "```json\n{\"pass\": true, \"reasoning\": \"\"}\n```")
	assert.Equal(t, 0, code, "a fenced passing verdict must still be accepted")
}

// reasonFromVerifierOutput recovers the reasoning line the script prints — the
// path a judge's refusal reason travels back to the agent.
func TestReasonFromVerifierOutput(t *testing.T) {
	stderr := "sr-agent: verifier (attempt 1/2): JUDGE-REASON: the proof is missing\n"
	assert.Equal(t, "the proof is missing", reasonFromVerifierOutput([]byte(stderr)))

	// The LAST reasoning wins (the final attempt's).
	multi := "JUDGE-REASON: first try\nJUDGE-REASON: final answer\n"
	assert.Equal(t, "final answer", reasonFromVerifierOutput([]byte(multi)))

	// No marker: empty.
	assert.Equal(t, "", reasonFromVerifierOutput([]byte("nothing here")))
}

// A judge that never wrote its verdict is refused with ONE fixed sentence, not
// with sr-agent's whole stderr (its banner, a harness warning, a deleted temp
// path).
func TestJudgeRefusalReason_NeverWrittenVerdictIsAFixedReason(t *testing.T) {
	stderr := []byte("sr-agent: harness claude-code, model sonnet (from \"size-md\")\n" +
		"Warning: something the harness printed\n" +
		"sr-agent: check failed: the agent wrote no output to /var/folders/x/T/sr-agent-output123/answer (after 2 attempts)\n")
	assert.Equal(t, "the judge did not produce a JSON verdict object", judgeRefusalReason(nil, stderr))
}

// An older sr-agent on PATH that does not know a judge's flags is named as such.
func TestJudgeRefusalReason_OlderSrAgentIsNamed(t *testing.T) {
	stderr := []byte("Error: unknown flag: --add-dir:readonly\nIf that was meant to be the prompt ...\n")
	got := judgeRefusalReason(nil, stderr)
	assert.Contains(t, got, "older than this engine")
	assert.NotContains(t, got, "If that was meant")
}

// A verifier's own reasoning still wins over both.
func TestJudgeRefusalReason_VerifierReasoningWins(t *testing.T) {
	stderr := []byte("sr-agent: verifier (attempt 1/2): JUDGE-REASON: the change drops field x\n")
	assert.Equal(t, "the change drops field x", judgeRefusalReason(nil, stderr))
}

// A judge that produced no parseable answer is a typed NoVerdict refusal; a real refusal is not.
func TestJudgeRefusal_NoVerdictIsTyped(t *testing.T) {
	none := judgeRefusal(nil, []byte("sr-agent: the agent wrote no output to /x"))
	assert.True(t, none.Refused)
	assert.True(t, none.NoVerdict)
	real := judgeRefusal(nil, []byte("sr-agent: verifier (attempt 1/1): JUDGE-REASON: the ADR is not cited"))
	assert.True(t, real.Refused)
	assert.False(t, real.NoVerdict)
}

// Only the verifier's own reasoning of a rejected verdict is a verdict; every other failure of
// the judge (old sr-agent, the model's or transport's error text, a blank) is NoVerdict too.
func TestJudgeRefusal_OnlyAVerifierReasonIsAVerdict(t *testing.T) {
	assert.False(t, judgeRefusal(nil, []byte("sr-agent: verifier (attempt 1/2): JUDGE-REASON: the change drops field x\n")).NoVerdict)
	for _, stderr := range []string{
		"Error: unknown flag: --add-dir:readonly\n",
		"API Error: 529 overloaded_error\n",
		"",
	} {
		assert.True(t, judgeRefusal(nil, []byte(stderr)).NoVerdict, stderr)
	}
}

// A multi-line reasoning (a numbered list of every failing item) survives the verifier's
// output whole: the script JSON-encodes it on the marker line and the reader decodes it.
func TestVerifier_MultilineReasoningSurvivesRoundTrip(t *testing.T) {
	want := "1. the ADR is not cited\n2. the migration is missing\n3. the test asserts nothing"
	verdict, err := json.Marshal(map[string]any{"pass": false, "reasoning": want})
	require.NoError(t, err)
	code, stderr := runVerifier(t, string(verdict))
	assert.Equal(t, 3, code)
	assert.Equal(t, want, reasonFromVerifierOutput([]byte("sr-agent: verifier (attempt 1/1): "+stderr)))

	pass, err := json.Marshal(map[string]any{"pass": true, "reasoning": "line one\nline two"})
	require.NoError(t, err)
	code, stderr = runVerifier(t, string(pass))
	assert.Equal(t, 0, code)
	assert.Equal(t, "line one\nline two", passReasonFromVerifierOutput([]byte(stderr)))
}

func TestReasonFromVerifierOutput_EncodedForms(t *testing.T) {
	// Multi-line, decoded whole.
	enc := "sr-agent: verifier (attempt 1/1): JUDGE-REASON-JSON: \"a\\nb\\nc\"\n"
	assert.Equal(t, "a\nb\nc", reasonFromVerifierOutput([]byte(enc)))

	// Empty encoded reasoning is no reasoning.
	assert.Equal(t, "", reasonFromVerifierOutput([]byte("JUDGE-REASON-JSON: \"\"\n")))
	assert.Equal(t, "", passReasonFromVerifierOutput([]byte("JUDGE-PASS-REASON-JSON: \"\"\n")))

	// A marker inside the reasoning's own text is text, not a second reasoning.
	tricky := "JUDGE-REASON-JSON: \"see JUDGE-REASON: x\\nand JUDGE-REASON-JSON: y\"\n"
	assert.Equal(t, "see JUDGE-REASON: x\nand JUDGE-REASON-JSON: y", reasonFromVerifierOutput([]byte(tricky)))

	// Old single-line output still reads, and the last reasoning wins across forms.
	old := "JUDGE-REASON: first try\nJUDGE-REASON-JSON: \"final\\nanswer\"\n"
	assert.Equal(t, "final\nanswer", reasonFromVerifierOutput([]byte(old)))
	assert.Equal(t, "plain old pass", passReasonFromVerifierOutput([]byte("JUDGE-PASS-REASON: plain old pass\n")))
}
