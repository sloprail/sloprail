package dispatch

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These cover the killed-by-signal diagnosis in the script path: runShell must
// recover the signal that ExitCode() flattens to -1, and scriptRefusalReason must
// turn it into a "killed" message rather than the "exit -1 … no reason" the code
// alone would yield. The e2e sibling (tests/e2e/harness/fileguard/036_killed_by_signal)
// proves the same fact end to end through the real dispatch and the agent stream;
// this pins the two engine seams directly and cheaply.

// runShell recovers the SIGNAL of a process the OS killed, which ExitCode() alone
// reports as a bare -1. A check that SIGKILLs its own shell is the stand-in for
// any violent death (a crash, an OOM kill, an outside `kill`).
// sr:proves checks/check-that-cannot-answer-refuses
func TestRunShell_RecoversKillingSignal(t *testing.T) {
	_, _, code, expired, signal, startErr := runShell("", "kill -9 $$", nil, nil, 0)

	require.NoError(t, startErr, "the shell started; it is the signal death under test")
	assert.False(t, expired, "this died at once, not on the timeout — the timeout path must not claim it")
	assert.Equal(t, -1, code, "ExitCode() flattens a signalled death to -1")
	assert.Equal(t, syscall.SIGKILL, signal, "the real signal must be recovered, not lost behind -1")
}

// An ordinary non-zero exit carries NO signal — the signal channel is 0, so
// scriptRefusalReason does not misreport a plain `exit 1` as a kill.
func TestRunShell_OrdinaryExitCarriesNoSignal(t *testing.T) {
	_, _, code, expired, signal, startErr := runShell("", "exit 3", nil, nil, 0)

	require.NoError(t, startErr)
	assert.False(t, expired)
	assert.Equal(t, 3, code)
	assert.Equal(t, syscall.Signal(0), signal, "a process that chose its exit was not signalled")
}

// scriptRefusalReason says the check was KILLED (and names the signal) for a
// signalled death, in place of the regressed "exit -1 … no reason".
// sr:proves checks/check-that-cannot-answer-refuses
func TestScriptRefusalReason_KilledBySignalSaysKilled(t *testing.T) {
	got := scriptRefusalReason("./crash.sh", -1, syscall.SIGKILL, nil, nil)

	assert.Contains(t, got, "killed", "the reason must say the check was killed, not that it decided")
	assert.NotContains(t, got, "exit -1", "the -1 sentinel must not be reported as a status a process can return")
	// The signal is named so an author can tell a SIGKILL (an OOM/`kill -9`) from a
	// SIGSEGV (a crash in the check itself).
	assert.Contains(t, got, "signal 9")
}

// A signalled death does NOT wear the timeout wording. The timeout is reported
// before scriptRefusalReason is ever reached (runScriptExec's expired branch), so
// the killed message must not claim a deadline the engine did not impose.
// sr:proves checks/check-that-cannot-answer-refuses
func TestScriptRefusalReason_KilledIsNotTimeout(t *testing.T) {
	got := scriptRefusalReason("./crash.sh", -1, syscall.SIGKILL, nil, nil)

	assert.NotContains(t, got, "did not answer", "that is the timeout's wording, not a crash's")
	assert.NotContains(t, got, "time limit")
}

// An ordinary non-zero exit with nothing to say still gets the bare-exit fallback
// — the signal branch must not steal it (signal is 0 here).
func TestScriptRefusalReason_OrdinaryExitKeepsBareFallback(t *testing.T) {
	got := scriptRefusalReason("./check.sh", 3, 0, nil, nil)

	assert.Contains(t, got, "exit 3")
	assert.NotContains(t, got, "killed")
}

// runScriptExec, end to end on the real OS: a check that SIGKILLs itself refuses
// (fail-closed preserved) with a "killed" reason and no "exit -1". This is the
// whole script path — runShell's signal recovery feeding scriptRefusalReason —
// exercised against a genuinely signalled process rather than a synthesised code.
// sr:proves checks/check-that-cannot-answer-refuses
func TestRunScriptExec_SignalledCheckRefusesWithKilled(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "k.sh"), []byte("#!/bin/sh\nkill -9 $$\n"), 0o755))
	res, err := runScriptExec(scriptCall{
		Dir:    dir,
		Script: "./k.sh",
	})
	require.NoError(t, err)

	assert.False(t, res.Passed, "a check the OS killed must refuse (fail-closed)")
	assert.True(t, strings.Contains(res.Reason, "killed"), "the reason must say killed: %q", res.Reason)
	assert.False(t, strings.Contains(res.Reason, "exit -1"), "the reason must not report exit -1: %q", res.Reason)
}

// A check that cannot even be STARTED is reported by runShell as a start error,
// distinct from every could-not-run status a shell that DID start would report by
// exit code (126/127). A NUL byte in the command makes fork/exec reject the argv
// before the shell runs — the stand-in for any launch failure (a Dir that went
// away, an unloadable interpreter). This is the seam runScriptExec's fail-closed
// start-error branch stands on, and no test reached it before.
func TestRunShell_CannotStartReturnsStartErr(t *testing.T) {
	_, _, code, expired, signal, startErr := runShell("", "echo hi\x00rest", nil, nil, 0)

	require.Error(t, startErr, "a command that cannot be launched must surface a start error, not a clean run")
	assert.False(t, expired, "a launch failure is not a timeout — the timeout path must not claim it")
	assert.Equal(t, -1, code, "a process that never started has no exit code")
	assert.Equal(t, syscall.Signal(0), signal, "a process that never started was not signalled")
}

// runScriptExec, end to end: a check that cannot START refuses (fail-closed), and
// folds the launch failure into the Verdict rather than erroring up to the caller
// — the exec.go:127 property the Wave-3 audit names, that a check which cannot run
// must not be read as approval. The error return is nil BECAUSE the refusal is the
// answer: the old dispatch returned an error here and refused at the call site;
// this keeps every script outcome one shape (a Verdict), so no caller re-decides.
// sr:proves checks/check-that-cannot-answer-refuses
func TestRunScriptExec_CannotStartRefuses(t *testing.T) {
	res, err := runScriptExec(scriptCall{
		Dir:    t.TempDir(),
		Script: "echo hi\x00rest", // a NUL byte: fork/exec rejects the argv before the shell runs.
	})
	require.NoError(t, err, "a launch failure must be folded into a refusal, not raised to the caller")

	assert.False(t, res.Passed, "a check that cannot start must refuse (fail-closed)")
	assert.Contains(t, res.Reason, "could not be run",
		"the refusal must say the check could not be run")
	assert.Contains(t, res.Reason, "refused because a check that cannot run must not be read as approval",
		"the refusal must state why a check that cannot run is not approval")
}

// SR_GUARDRAIL_DIR must be ABSOLUTE, per its own doc comment on scriptCall.env
// ("so a script can find its siblings by absolute path"). Measured in a real
// dispatch to arrive RELATIVE at least once — Go's exec.Cmd.Dir silently
// resolves a relative directory against the PARENT process's own cwd at spawn
// time, so a relative scriptCall.Dir placed the child in the right directory
// by coincidence (the parent happened to already be there) while the env var
// carried that same relative string into a process with no way to reconstruct
// what the parent's cwd had been — authoring-slop's judge prepare read a
// relative SR_GUARDRAIL_DIR, joined it onto its own (already-correct) cwd, and
// searched a doubled path that matched nothing, refusing every real edit with
// "judge-rules/ contains no rule with 'enforced: true'" regardless of content.
//
// This pins the fix at the seam: scriptCall.Dir passed RELATIVE (resolvable
// only because the test's own process cwd is chdir'd to Dir's parent first,
// mirroring the coincidence that masked the bug in production), and the
// script echoes $SR_GUARDRAIL_DIR back out — asserted to equal the real
// absolute directory, not the relative string that was passed in.
func TestRunScriptExec_GuardrailDirEnvIsAlwaysAbsolute(t *testing.T) {
	absDir := t.TempDir()
	// macOS reports /var where the filesystem holds /private/var; filepath.Abs
	// (what the fix under test calls) does not resolve that symlink, so this
	// test's own comparison must not either — otherwise it would fail on a
	// correct result, the same class of mismatch workspaceAnchor's own doc
	// comment (services/sr-session/statedir.go) already names.
	if resolved, err := filepath.EvalSymlinks(absDir); err == nil {
		absDir = resolved
	}
	parent := filepath.Dir(absDir)
	relDir := filepath.Base(absDir)

	origWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(parent))
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	require.NoError(t, os.WriteFile(filepath.Join(absDir, "echo.sh"), []byte("#!/bin/sh\necho \"$SR_GUARDRAIL_DIR\"\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(absDir, "verify.sh"), []byte(`#!/bin/sh
if [ "$SR_GUARDRAIL_DIR" != "`+absDir+`" ]; then
	echo "SR_GUARDRAIL_DIR was '$SR_GUARDRAIL_DIR', want '`+absDir+`'" >&2
	exit 1
fi
`), 0o755))
	res, err := runScriptExec(scriptCall{
		Dir:    relDir, // relative, resolvable only against the parent's cwd above
		Script: "./echo.sh",
	})
	require.NoError(t, err)
	require.True(t, res.Passed, "echo must succeed: %q", res.Reason)

	// scriptResult carries no stdout field for a passing check (only Reason, on
	// refusal), so the env var is proven via a second call that fails on
	// purpose whenever the value is not the expected absolute path — turning
	// "what the child actually saw" into the refusal reason runScriptExec
	// already surfaces.
	verify, err := runScriptExec(scriptCall{
		Dir:    relDir,
		Script: "./verify.sh",
	})
	require.NoError(t, err)
	assert.True(t, verify.Passed,
		"SR_GUARDRAIL_DIR must be absolute even when scriptCall.Dir is relative: %s", verify.Reason)
}

// A declared script runs DIRECTLY: one without the execute bit, without a shebang
// or with a non-standard interpreter is refused, naming the file and the fix —
// never run through `sh <file>`.
// sr:proves checks/check-that-cannot-answer-refuses
func TestRunScript_RefusesWhatCannotBeExecedDirectly(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []struct {
		name, body string
		mode       os.FileMode
		fix        string
	}{
		{"noexec.sh", "#!/bin/sh\nexit 0\n", 0o644, "chmod +x"},
		{"bare.sh", "exit 0\n", 0o755, "#!/usr/bin/env bash"},
		{"local.sh", "#!/usr/local/bin/bash\nexit 0\n", 0o755, "#!/usr/bin/env bash"},
	} {
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte(f.body), f.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(dir, f.name), f.mode); err != nil {
			t.Fatal(err)
		}
		res, err := runScriptExec(scriptCall{Dir: dir, Script: "./" + f.name})
		if err != nil {
			t.Fatalf("%s: %v", f.name, err)
		}
		if res.Passed || !strings.Contains(res.Reason, f.name) || !strings.Contains(res.Reason, f.fix) {
			t.Errorf("%s: want a refusal naming the file and %q, got passed=%v %q", f.name, f.fix, res.Passed, res.Reason)
		}
	}
	ok := filepath.Join(dir, "ok.sh")
	if err := os.WriteFile(ok, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if res, _ := runScriptExec(scriptCall{Dir: dir, Script: "./ok.sh"}); !res.Passed {
		t.Errorf("a well-formed script must pass: %s", res.Reason)
	}
}

// SR_AGENT_ID carries the sub-agent a hook fired inside, and is set even when
// empty: a stale id inherited from an outer process (a check that launched an
// agent hands its environment down) must not tell a main-session check it runs
// inside a sub-agent.
// sr:proves subagents/subagent-id-reaches-checks
func TestRunScriptExec_AgentIDEnvOverridesInherited(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "want.sh"), []byte(`#!/bin/sh
[ "${SR_AGENT_ID-unset}" = "$WANT" ] || { echo "SR_AGENT_ID was '${SR_AGENT_ID-unset}', want '$WANT'" >&2; exit 1; }
`), 0o755))
	t.Setenv("SR_AGENT_ID", "stale-outer-agent")
	for _, id := range []string{"a17b8b227a41c5192", ""} {
		res, err := runScriptExec(scriptCall{Dir: dir, Script: "./want.sh", AgentID: id, Env: []string{"WANT=" + id}})
		require.NoError(t, err)
		assert.True(t, res.Passed, "agent id %q: %s", id, res.Reason)
	}
}
