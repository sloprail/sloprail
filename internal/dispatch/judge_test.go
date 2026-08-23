package dispatch

import (
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
	assert.Contains(t, stderr, "JUDGE-REASON:", "the reasoning is surfaced for the engine to recover")
	assert.Contains(t, stderr, "the proof is missing")
	assert.NotContains(t, stderr, "not a boolean", "a real false must not be misread as a malformed verdict")
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
