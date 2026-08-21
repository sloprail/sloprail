// Package e2e drives every way a check can fail, and asserts none of them
// permits.
//
// 004 established the principle on five cases: stderr refusal, non-executable,
// silent non-zero, empty JSON reason, and the exit-zero control. This widens it
// to the rest of the surface, because "a guardrail must never fail open" is a
// claim about EVERY failure mode, and each untested one is a way the mechanism
// can break into silent permission.
//
// The modes added here: a missing binary, a check killed by a signal, output on
// both channels at once, malformed JSON on stdout, binary bytes on stdout, a
// check that is a symlink, and a check that prints a refusal and then exits zero.
//
// That last one is the interesting inversion, and the only case in this file
// where the work PROCEEDS. It is not a fail-open: the check exited zero, and
// exit zero is how a check says yes. What it demonstrates is that the refusal
// vocabulary carries no authority of its own — a script that means to refuse
// must exit non-zero, and one that prints a refusal and returns success has
// permitted the work and told nobody.
//
// Two things every test below asserts together: that the refusal was reported,
// and that the tree is unchanged. Only the second is what a guardrail is for.
// These use the harness's Refused(), which reads the harness's own marker.
//
// # RE-VEHICLED onto the NEW gate nature (was old GUARDRAIL.md hooks)
//
// The fail-closed discipline is the new check-runner's own (internal/dispatch:
// a check that cannot run, times out, or exits non-zero is a refusal), and it is
// reached identically whether a gate or a file-guard invokes it. The vehicle is a
// GATE on the pre-write event — one-shot, so a permitting check records exactly
// once (no after-check to double it), which the ledger-counting cases below rely
// on. Refusals are observed with res.Refused() and the tree; a gate's own ledger
// under `.sloprail/gate/<name>/` proves the check ran.
//
// One failure mode from the old suite is NOT re-vehicled here: a SIGNAL-killed
// check saying "killed" rather than "exit -1" (old T019_09e). The new
// check-runner's scriptRefusalReason has no killedBySignal branch, so a crash is
// reported as "refused (exit -1) but gave no reason" — it still REFUSES (T019_02
// pins that), but the message regresses. That is reported as a precise gap rather
// than re-vehicled to a weaker assertion.

package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bindEveryWrite is a gate that runs one check on every creation. Only the check
// varies.
const bindEveryWrite = `on:
  - event: PreFileCreate
checks:
  - script: ./h.sh
`

// mustNotPermit runs one failing check and asserts the work was stopped.
//
// A helper rather than seven copies, because the assertion is identical every
// time and the thing that varies is the script. It returns nothing; a caller
// wanting extra assertions installs the gate itself.
func mustNotPermit(t *testing.T, session, gate, script string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.Gate(proj, gate, bindEveryWrite, map[string]string{"h.sh": script})

	res := e.Run(proj, session, "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	assert.True(t, res.Refused(),
		"a check that failed this way was read as approval:\n%s", res.Output)
	assert.False(t, e.Exists(proj, "notes.md"),
		"the work went through despite the guardrail failing — this is the fail-open")
}

// T019_01: a check whose command does not exist refuses.
//
// Exit 127. Distinct from a non-executable file (126): there the file is
// present and cannot be run, here there is no file at all — a rule whose script
// was renamed, or whose declaration names the wrong one. Both are ways an
// author disarms their own rule by accident, and neither may read as consent.
func TestT019_01_AMissingHookRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	// Declared, but no script written for it.
	e.Gate(proj, "missing", bindEveryWrite, map[string]string{})

	res := e.Run(proj, "s-019-01", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	assert.True(t, res.Refused(), "a check that does not exist was read as approval:\n%s", res.Output)
	assert.False(t, e.Exists(proj, "notes.md"), "the work went through with no rule having run")
	assert.True(t, res.Saw("not found"),
		"the refusal must say what is wrong, or the author cannot find the missing file")
}

// T019_02: a check killed by a signal refuses.
//
// A crash rather than a decision — a segfault, an OOM kill, a `kill -9`. The
// process produced no exit status of its own. Nothing about that is a verdict,
// and treating it as one would mean a rule whose check crashes on some inputs
// silently permits exactly those inputs.
func TestT019_02_AHookKilledBySignalRefuses(t *testing.T) {
	mustNotPermit(t, "s-019-02", "killed", "#!/bin/sh\ncat >/dev/null\nkill -9 $$\n")
}

// T019_03: a check writing to BOTH channels and failing refuses, and the agent
// is told something usable.
//
// A real script logs progress to stdout and its reason to stderr, and the engine
// reads both — so the question is not only whether it refuses but which text
// becomes the reason. stdout wins where it has prose (a check that writes there
// is answering); this pins that the choice is made rather than the two being
// concatenated or one being dropped.
func TestT019_03_AHookOnBothChannelsRefusesWithUsableText(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "chatty", bindEveryWrite, map[string]string{
		"h.sh": "#!/bin/sh\ncat >/dev/null\necho 'REASON-on-stdout'\necho 'REASON-on-stderr' >&2\nexit 1\n",
	})

	res := e.Run(proj, "s-019-03", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	require.True(t, res.Refused(), "a check writing to both channels was read as approval")
	assert.False(t, e.Exists(proj, "notes.md"), "the work must be prevented")
	assert.True(t, res.Saw("REASON-on-stdout"),
		"the agent must be told something it can act on, and stdout prose is what a check answers with")
}

// T019_04: malformed JSON on stdout refuses, and is not shown raw.
//
// A check that meant to emit a verdict document and truncated it — a killed
// pipeline, a partial write. The bytes parse as neither a verdict nor prose,
// and the engine must fall back to refusing with something of its own rather
// than either permitting or handing the agent a fragment of JSON.
func TestT019_04_MalformedJSONOnStdoutRefusesWithoutLeakingIt(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "truncated", bindEveryWrite, map[string]string{
		"h.sh": "#!/bin/sh\ncat >/dev/null\nprintf '{\"reason\":\"blo'\nexit 1\n",
	})

	res := e.Run(proj, "s-019-04", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	require.True(t, res.Refused(), "a check emitting malformed JSON was read as approval")
	assert.False(t, e.Exists(proj, "notes.md"), "the work must be prevented")
	assert.True(t, res.Saw("truncated"),
		"the refusal must still name the guardrail, so the agent can find the rule")
}

// T019_05: binary bytes on stdout refuse.
//
// A check that ran the wrong program and emitted an object file, or a script
// whose output went through a compressor. The bytes are not text, not JSON, and
// may contain NULs — which is exactly the shape that breaks a naive string
// pipeline, and the shape whose mishandling could produce a permit.
func TestT019_05_BinaryOutputRefuses(t *testing.T) {
	mustNotPermit(t, "s-019-05", "binary",
		"#!/bin/sh\ncat >/dev/null\nprintf 'refused\\000\\001\\002\\003'\nexit 1\n")
}

// T019_06: a check that is a SYMLINK to a script elsewhere still runs, and its
// refusal still governs.
//
// The one case in this file that must PERMIT the mechanism rather than refuse
// it: a symlinked check is a legitimate arrangement — a project sharing one
// script across several guardrails rather than copying it — and an engine that
// refused to follow it would break a working setup while looking like a
// security measure.
//
// So this asserts the check RAN, by its refusal reaching the agent. A test that
// only asserted "refused" would also pass if the engine refused BECAUSE it
// could not run a symlink, which is the opposite outcome with the same
// signature. The distinctive reason text is what tells the two apart.
func TestT019_06_ASymlinkedHookRunsAndItsRefusalGoverns(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "linked", bindEveryWrite, map[string]string{})

	// The real script lives outside the gate folder; the check is a link.
	real := filepath.Join(proj, "shared-hook.sh")
	require.NoError(t, os.WriteFile(real,
		[]byte("#!/bin/sh\ncat >/dev/null\necho '{\"reason\":\"the shared hook refused\"}'\nexit 1\n"), 0o755))
	require.NoError(t, os.Symlink(real,
		filepath.Join(proj, ".sloprail", "gate", "linked", "h.sh")))

	res := e.Run(proj, "s-019-06", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	assert.True(t, res.Saw("the shared hook refused"),
		"a symlinked check must actually be RUN — refusing because it is a link is a different failure")
	assert.True(t, res.Refused(), "and its refusal must govern")
	assert.False(t, e.Exists(proj, "notes.md"), "and prevent the work")
}

// T019_07: a check that prints a refusal and exits zero PERMITS.
//
// The inversion, and the one case here where the work proceeds. Exit zero is
// how a check says yes; the refusal vocabulary is not a second channel that can
// override it. An engine that let printed text overrule a success status would
// hand every check that merely MENTIONS a refusal — a diagnostic, an echoed
// input, a log line — the power to block work its author meant to allow.
//
// Both halves are asserted, and the second is what makes this about the failure
// SURFACE rather than about protocol trivia: the objection reaches nobody. A
// check author who writes this has not made their rule advisory, they have made
// it silent, and the write lands with no trace of the objection anywhere the
// agent can see.
func TestT019_07_ARefusalPrintedAtExitZeroPermitsAndIsNotDelivered(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "toothless", bindEveryWrite, map[string]string{
		"h.sh": "#!/bin/sh\ncat >/dev/null\necho ran >> \"$SR_GUARDRAIL_DIR/log\"\necho 'I refuse this write' >&2\nexit 0\n",
	})

	res := e.Run(proj, "s-019-07", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	// The rule really was asked. Without this the assertions below hold for a
	// gate that never loaded.
	require.Equal(t, []string{"ran"}, e.GateLedgerLines(proj, "toothless", "log"),
		"the check must have run, or this says nothing about what it decided")

	assert.False(t, res.Refused(), "exit zero permits — printed words are not a verdict")
	assert.True(t, e.Exists(proj, "notes.md"), "and the work proceeds")
	assert.False(t, res.Saw("I refuse this write"),
		"the objection reaches nobody: a check at exit 0 permits and its output is not a verdict channel")
}

// T019_08: a check that permits is not made to refuse by any of this.
//
// The control the whole file needs. Seven tests above assert "the work was
// stopped", and every one of them passes on an engine that stops everything. A
// guardrail refusing work its check approved is as broken as one approving work
// its check refused, and only this tells the two fixes apart.
//
// It is deliberately noisy on both channels while exiting zero, because that is
// the shape closest to the failures above — if any of them were implemented as
// "output means refusal" rather than "status means refusal", this is the test
// that catches it.
func TestT019_08_APermittingHookIsStillPermitted(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "permits", bindEveryWrite, map[string]string{
		"h.sh": "#!/bin/sh\ncat >/dev/null\necho 'chatter on stdout'\necho 'chatter on stderr' >&2\nexit 0\n",
	})

	res := e.Run(proj, "s-019-08", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	assert.False(t, res.Refused(), "a check that exited zero was treated as refusing:\n%s", res.Output)
	assert.True(t, e.Exists(proj, "notes.md"), "and the permitted write must land")
}
