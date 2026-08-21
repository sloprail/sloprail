package dispatch

import (
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These cover the killed-by-signal diagnosis in the script path: runShell must
// recover the signal that ExitCode() flattens to -1, and scriptRefusalReason must
// turn it into a "killed" message rather than the "exit -1 … no reason" the code
// alone would yield. The e2e sibling (tests/e2e/fileguard/036_killed_by_signal)
// proves the same fact end to end through the real dispatch and the agent stream;
// this pins the two engine seams directly and cheaply.

// runShell recovers the SIGNAL of a process the OS killed, which ExitCode() alone
// reports as a bare -1. A check that SIGKILLs its own shell is the stand-in for
// any violent death (a crash, an OOM kill, an outside `kill`).
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
func TestRunScriptExec_SignalledCheckRefusesWithKilled(t *testing.T) {
	dir := t.TempDir()
	res, err := runScriptExec(scriptCall{
		Dir:    dir,
		Script: "kill -9 $$",
	})
	require.NoError(t, err)

	assert.False(t, res.Passed, "a check the OS killed must refuse (fail-closed)")
	assert.True(t, strings.Contains(res.Reason, "killed"), "the reason must say killed: %q", res.Reason)
	assert.False(t, strings.Contains(res.Reason, "exit -1"), "the reason must not report exit -1: %q", res.Reason)
}
