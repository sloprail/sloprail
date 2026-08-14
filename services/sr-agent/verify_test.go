package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeScript drops an executable script and returns its path.
func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755))
	return path
}

func requireSh(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
}

// runVerifyHarness drives the real runVerified loop against a fake harness.
//
// The fake stands in for `claude`: it is a script, so a test can make the agent
// behave any way a test needs — write a bad answer, write nothing, or write a
// different answer on the second attempt. The whole retry loop, the prompt
// rewriting and the verifier contract are the real code.
//
// The fake learns where to write from SR_TEST_OUTPUT, which runVerified does
// not set — a REAL agent learns the path from the prompt, and a fake cannot
// read a prompt. The path is discovered by watching for the file runVerified
// creates in the temp dir it makes.
func runVerifyHarness(
	t *testing.T, spec harnessSpec, verifier, prompt string, attempts int, dir string,
) (stdout, stderr string, err error) {
	t.Helper()

	cmd := newRoot()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))

	// The output directory is pinned so the fake knows where to write. A real
	// agent learns the path from the prompt; a script cannot read a prompt.
	outputDir := filepath.Join(dir, "out")
	require.NoError(t, os.MkdirAll(outputDir, 0o755))
	t.Setenv(outputDirEnv, outputDir)
	t.Setenv("SR_TEST_OUTPUT", filepath.Join(outputDir, "answer"))

	err = runVerified(cmd, spec, "fake-model", nil, prompt, verifier, attempts, false, false)
	return out.String(), errOut.String(), err
}

// --- resolving the verifier ------------------------------------------------

func TestResolveVerifier_ExecutableFile(t *testing.T) {
	requireSh(t)
	path := writeScript(t, t.TempDir(), "ok.sh", "exit 0\n")

	got, err := ResolveVerifier(path)
	require.NoError(t, err)
	assert.Equal(t, path, got)
}

// A non-executable script is REFUSED rather than run through a shell: guessing
// an interpreter reports a Python SyntaxError as a shell parse error.
func TestResolveVerifier_NonExecutableIsRefusedWithTheFix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "check.py")
	require.NoError(t, os.WriteFile(path, []byte("import sys\n"), 0o644))

	_, err := ResolveVerifier(path)
	require.ErrorIs(t, err, ErrVerifierBroken)
	assert.Contains(t, err.Error(), "chmod +x", "the message must name the fix")
}

func TestResolveVerifier_MissingFileIsRefused(t *testing.T) {
	_, err := ResolveVerifier(filepath.Join(t.TempDir(), "nope.sh"))
	require.ErrorIs(t, err, ErrVerifierBroken)
}

func TestResolveVerifier_DirectoryIsRefused(t *testing.T) {
	_, err := ResolveVerifier(t.TempDir())
	require.ErrorIs(t, err, ErrVerifierBroken)
	assert.Contains(t, err.Error(), "directory")
}

func TestResolveVerifier_EmptyIsRefused(t *testing.T) {
	_, err := ResolveVerifier("   ")
	require.ErrorIs(t, err, ErrVerifierBroken)
}

// A bare name resolves on PATH, as a shell would.
func TestResolveVerifier_BareNameFromPath(t *testing.T) {
	requireSh(t)
	got, err := ResolveVerifier("true")
	require.NoError(t, err)
	assert.NotEmpty(t, got)
}

func TestResolveVerifier_BareNameNotOnPathIsRefused(t *testing.T) {
	_, err := ResolveVerifier("sr-agent-definitely-not-a-real-command")
	require.ErrorIs(t, err, ErrVerifierBroken)
	assert.Contains(t, err.Error(), "PATH")
}

// --- what the verifier receives --------------------------------------------

// The contract, pinned: argv[1], the env var, stdin, and the attempt counters.
// A verifier is the caller's own code and every one of these is load-bearing
// for somebody — a shell script reaches for "$1", a Python one for os.environ,
// and a `jq -e` one for stdin.
func TestRunVerifier_ReceivesPathAndContentAndAttempt(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()

	record := filepath.Join(dir, "record")
	script := writeScript(t, dir, "v.sh", fmt.Sprintf(`
{
  echo "argv1=$1"
  echo "env=$%s"
  echo "attempt=$%s of $%s"
  echo "stdin=$(cat)"
} > %s
exit 0
`, OutputPathEnv, AttemptEnv, AttemptsEnv, record))

	output := filepath.Join(dir, "answer")
	require.NoError(t, os.WriteFile(output, []byte(`{"pass":true}`), 0o600))

	var stderr bytes.Buffer
	require.NoError(t, RunVerifier(context.Background(), script, output, 1, 2, &stderr))

	got, err := os.ReadFile(record)
	require.NoError(t, err)
	text := string(got)

	assert.Contains(t, text, "argv1="+output)
	assert.Contains(t, text, "env="+output)
	assert.Contains(t, text, "attempt=1 of 2")
	assert.Contains(t, text, `stdin={"pass":true}`)
}

// Exit zero passes, non-zero fails. That is the convention every shell check
// already uses, so `jq -e`, `test` and `grep -q` work unadapted.
func TestRunVerifier_ExitStatusIsTheVerdict(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()
	output := filepath.Join(dir, "answer")
	require.NoError(t, os.WriteFile(output, []byte("content"), 0o600))

	var stderr bytes.Buffer
	pass := writeScript(t, dir, "pass.sh", "exit 0\n")
	require.NoError(t, RunVerifier(context.Background(), pass, output, 1, 1, &stderr))

	fail := writeScript(t, dir, "fail.sh", "echo 'missing the verdict field' >&2\nexit 1\n")
	err := RunVerifier(context.Background(), fail, output, 1, 1, &stderr)
	require.ErrorIs(t, err, ErrVerifyFailed)
	assert.Contains(t, err.Error(), "missing the verdict field",
		"the verifier's complaint must survive, it is what the retry is told")
}

// An agent that wrote nothing is a verification FAILURE, not a broken verifier:
// it was told to write the file and did not, which is what --verify is for.
func TestRunVerifier_MissingOutputFileIsAFailureNotABrokenVerifier(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()
	script := writeScript(t, dir, "v.sh", "exit 0\n")

	var stderr bytes.Buffer
	err := RunVerifier(context.Background(), script,
		filepath.Join(dir, "never-written"), 1, 1, &stderr)

	require.ErrorIs(t, err, ErrVerifyFailed)
	assert.NotErrorIs(t, err, ErrVerifierBroken)
}

// FAIL-CLOSED. A verifier that cannot execute has checked nothing, and calling
// that a pass is how a guardrail stops guarding without anyone noticing.
func TestRunVerifier_UnrunnableVerifierFailsClosed(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "answer")
	require.NoError(t, os.WriteFile(output, []byte("x"), 0o600))

	var stderr bytes.Buffer
	err := RunVerifier(context.Background(), filepath.Join(dir, "not-a-binary"), output, 1, 1, &stderr)

	require.Error(t, err, "an unrunnable verifier must never be treated as a pass")
	assert.ErrorIs(t, err, ErrVerifierBroken)
}

// --- the retry loop ---------------------------------------------------------

// The core promise: a rejected answer means the agent is asked AGAIN, and the
// second attempt is told what the verifier complained about.
func TestVerified_RejectionRetriesAndTellsTheAgentWhy(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()

	// A fake harness standing in for `claude`: it writes whatever prompt it was
	// given into the output file. Its LAST argument is the prompt, so a prompt
	// carrying the verifier's complaint proves the retry was informed.
	fakeHarness := writeScript(t, dir, "fake-claude.sh", `
for last; do :; done
printf '%s' "$last" > "$SR_TEST_OUTPUT"
exit 0
`)

	// Rejects until the prompt it sees mentions the complaint.
	verifier := writeScript(t, dir, "v.sh", `
if grep -q "REJECTED" "$1"; then exit 0; fi
echo "the answer must contain the token BANANA" >&2
exit 1
`)

	spec := harnessSpec{name: "fake", binary: fakeHarness}
	out, errOut, err := runVerifyHarness(t, spec, verifier, "judge this", 2, dir)

	require.NoError(t, err, "the second attempt should pass. stderr: %s", errOut)
	assert.Contains(t, out, "REJECTED", "the retry prompt must reach the agent")
	assert.Contains(t, errOut, "BANANA", "the verifier's complaint must be surfaced")
	assert.Contains(t, errOut, "attempt 1/2")
	assert.Contains(t, errOut, "verified on attempt 2/2")
}

// Exhausting the attempts reports failure rather than passing the bad answer
// through — the whole point of a verifier is that its no is final.
func TestVerified_ExhaustedAttemptsFail(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()

	fakeHarness := writeScript(t, dir, "fake-claude.sh", `
printf 'always wrong' > "$SR_TEST_OUTPUT"
exit 0
`)
	verifier := writeScript(t, dir, "v.sh", "echo 'still wrong' >&2\nexit 1\n")

	spec := harnessSpec{name: "fake", binary: fakeHarness}
	out, _, err := runVerifyHarness(t, spec, verifier, "judge this", 2, dir)

	require.ErrorIs(t, err, ErrVerifyFailed)
	assert.Contains(t, err.Error(), "2 attempt")
	assert.NotContains(t, out, "always wrong",
		"a rejected answer must never reach stdout")
}

// A passing answer is what stdout carries — the file's contents, not the
// harness envelope, so a hook can read it directly.
func TestVerified_AcceptedAnswerIsStdout(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()

	fakeHarness := writeScript(t, dir, "fake-claude.sh", `
printf '{"pass":true,"why":"it holds"}' > "$SR_TEST_OUTPUT"
exit 0
`)
	verifier := writeScript(t, dir, "v.sh", "exit 0\n")

	spec := harnessSpec{name: "fake", binary: fakeHarness}
	out, _, err := runVerifyHarness(t, spec, verifier, "judge this", 2, dir)

	require.NoError(t, err)
	assert.JSONEq(t, `{"pass":true,"why":"it holds"}`, strings.TrimSpace(out))
}

// A broken verifier is not retried: asking the agent again cannot fix the
// caller's script, and each retry costs real money for a check not happening.
func TestVerified_BrokenVerifierIsNotRetried(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()

	counter := filepath.Join(dir, "runs")
	fakeHarness := writeScript(t, dir, "fake-claude.sh", fmt.Sprintf(`
echo x >> %s
printf 'answer' > "$SR_TEST_OUTPUT"
exit 0
`, counter))

	spec := harnessSpec{name: "fake", binary: fakeHarness}
	_, _, err := runVerifyHarness(t, spec, filepath.Join(dir, "absent-verifier"), "q", 3, dir)

	require.ErrorIs(t, err, ErrVerifierBroken)
	_, statErr := os.Stat(counter)
	assert.Error(t, statErr, "the agent must not run at all when the verifier cannot be resolved")
}

// The output file is truncated between attempts, or a second attempt that
// writes nothing would be judged on the answer that was already rejected.
func TestVerified_OutputIsResetBetweenAttempts(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()

	// Writes only on the first attempt; afterwards it leaves the file alone.
	marker := filepath.Join(dir, "ran-once")
	fakeHarness := writeScript(t, dir, "fake-claude.sh", fmt.Sprintf(`
if [ ! -f %s ]; then
  touch %s
  printf 'first answer' > "$SR_TEST_OUTPUT"
fi
exit 0
`, marker, marker))

	seen := filepath.Join(dir, "seen")
	verifier := writeScript(t, dir, "v.sh", fmt.Sprintf(`
cat "$1" >> %s
echo "---" >> %s
exit 1
`, seen, seen))

	spec := harnessSpec{name: "fake", binary: fakeHarness}
	_, _, err := runVerifyHarness(t, spec, verifier, "q", 2, dir)
	require.ErrorIs(t, err, ErrVerifyFailed)

	got, readErr := os.ReadFile(seen)
	require.NoError(t, readErr)
	assert.Equal(t, 1, strings.Count(string(got), "first answer"),
		"the stale answer must not be shown to the verifier a second time")
}

// --- CLI wiring -------------------------------------------------------------

func TestCLI_VerifyAttemptsMustBeAtLeastOne(t *testing.T) {
	requireSh(t)
	_, _, err := runCLI(t, underClaude, "--model", "size-md", "--dry-run",
		"--verify", "true", "--verify-attempts", "0", "q")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least 1")
}

// The other end of the range, which had no check at all.
//
// verify.go's own paragraph said the retry "is not unbounded, because an
// unbounded retry inside a Stop hook is how a guardrail becomes a bill" — and
// that described the DEFAULT while reading as a claim about the flag. The lower
// bound was enforced six lines below it and nothing enforced the upper, so
// `--verify-attempts 100000` was taken verbatim. It is not a slow no-op either:
// the loop in verify_run.go is serial, spawns a real `claude` per attempt, has
// no timeout, and on the REJECTING path never exits early — so it runs every one
// of them. The comment naming the failure sat directly above the flag allowing
// it.
func TestCLI_VerifyAttemptsHasAnUpperBound(t *testing.T) {
	requireSh(t)
	_, _, err := runCLI(t, underClaude, "--model", "size-md", "--dry-run",
		"--verify", "true", "--verify-attempts", "100000", "q")
	require.Error(t, err, "100000 real agent launches must be refused, not accepted")
	assert.Contains(t, err.Error(), "at most")
	assert.Contains(t, err.Error(), "100000", "the refusal must quote what was asked for")
}

// The bound is a property of the FLAG, not of the --verify path.
//
// The check used to sit inside `if cmd.Flags().Changed("verify")`, so the same
// `--verify-attempts 0` was an error with --verify and silently accepted
// without it — one flag with two meanings, and the permissive one reached by
// leaving a different flag off. Nothing downstream read the value there, so this
// pins the DIAGNOSTIC rather than a behaviour change: a caller who typed a
// number the tool will not honour is told so instead of having it ignored.
func TestCLI_VerifyAttemptsIsJudgedWithoutVerify(t *testing.T) {
	requireSh(t)
	for _, bad := range []string{"0", "-3", "100000"} {
		t.Run(bad, func(t *testing.T) {
			_, _, err := runCLI(t, underClaude, "--model", "size-md", "--dry-run",
				"--verify-attempts", bad, "q")
			require.Error(t, err,
				"--verify-attempts %s is refused under --verify and must not be accepted without it", bad)
		})
	}
}

// An unchanged flag is never judged, so the default cannot be refused by its own
// bound — and a legitimate override still runs.
func TestCLI_VerifyAttemptsAcceptsTheRange(t *testing.T) {
	requireSh(t)
	for _, ok := range []string{"1", "2", "20"} {
		t.Run(ok, func(t *testing.T) {
			_, _, err := runCLI(t, underClaude, "--model", "size-md", "--dry-run",
				"--verify", "true", "--verify-attempts", ok, "q")
			require.NoError(t, err, "%s is inside the range and must be accepted", ok)
		})
	}
}

// --dry-run must show BOTH halves: the agent command and the verifier that will
// judge it. Showing only the agent would hide the mechanism being configured.
func TestCLI_VerifyDryRunShowsBothCommands(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()
	script := writeScript(t, dir, "v.sh", "exit 0\n")

	stdout, _, err := runCLI(t, underClaude, "--model", "size-md", "--dry-run",
		"--verify", script, "judge this")
	require.NoError(t, err)

	assert.Contains(t, stdout, "claude -p --model sonnet")
	assert.Contains(t, stdout, script)
	// The agent must be told where to write, or it cannot satisfy the verifier.
	assert.Contains(t, stdout, "Write your answer to the file")
}

// The output file lives outside the working tree, and Claude Code refuses to
// write outside it. MEASURED, not theorised: without --add-dir the agent reads
// the file, computes the right answer, then says it needs permission — and the
// verifier judges an empty file, turning a correct judgement into a failure.
func TestCLI_VerifyGrantsWriteAccessToTheOutputDirectory(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()
	script := writeScript(t, dir, "v.sh", "exit 0\n")

	outputDir := filepath.Join(dir, "out")
	require.NoError(t, os.MkdirAll(outputDir, 0o755))
	t.Setenv(outputDirEnv, outputDir)

	stdout, _, err := runCLI(t, underClaude, "--model", "size-md", "--dry-run",
		"--verify", script, "judge this")
	require.NoError(t, err)

	assert.Contains(t, stdout, "--add-dir",
		"the agent cannot write its answer without being granted the directory")
	assert.Contains(t, stdout, outputDir)
}

func TestCLI_NonExecutableVerifierIsRefusedBeforeAnythingRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.sh")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o644))

	_, _, err := runCLI(t, underClaude, "--model", "size-md", "--verify", path, "q")
	require.ErrorIs(t, err, ErrVerifierBroken)
}

// --- the prompt the agent is given ------------------------------------------

// The file is the contract, so the instruction must be about the file.
func TestVerifyPromptSuffix_NamesTheFileAndTheStakes(t *testing.T) {
	suffix := verifyPromptSuffix("/tmp/x/answer")
	assert.Contains(t, suffix, "/tmp/x/answer")
	assert.Contains(t, suffix, "Write your answer")
}

// The retry prompt must quote the verifier's own words: an agent told only
// "wrong" produces a different wrong answer, one shown the complaint does not.
func TestRetryPromptSuffix_QuotesTheComplaint(t *testing.T) {
	suffix := retryPromptSuffix("/tmp/x/answer", "field 'pass' was missing")
	assert.Contains(t, suffix, "field 'pass' was missing")
	assert.Contains(t, suffix, "REJECTED")
}

func TestRetryPromptSuffix_HandlesASilentVerifier(t *testing.T) {
	suffix := retryPromptSuffix("/tmp/x/answer", "   ")
	assert.Contains(t, suffix, "no explanation")
}
