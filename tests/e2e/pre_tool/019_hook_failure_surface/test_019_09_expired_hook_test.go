package e2e

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T019_09: a check that never returns is killed, and the expiry refuses.
//
// The last failure mode on this surface, and the one that could not be written
// before. Every other test here fails FAST — a bad exit status, a missing file,
// a crash — and the engine sees a finished process. A check that hangs never
// finishes at all, so nothing in the fail-open discipline fires on its own: no
// failure is ever observed, because no answer is ever given.
//
// There are two nested deadlines here and only one of them is sloprail's, which
// is the whole design point. The HARNESS bounds the engine — a hook is run by a
// harness, so a harness decides how long the thing it launched may take, and
// real Claude Code does exactly that. The ENGINE bounds the guardrail check,
// because it is the engine that launched THAT. Getting this wrong is not a
// stylistic matter: with no bound of its own the engine waits on the wedged
// check until the harness kills the ENGINE, and an engine that has been killed
// has rendered no verdict at all — so the tool call proceeds, unjudged. The
// fail-open arrives by way of sloprail never getting to speak.
//
// So the engine's deadline must fire FIRST, and what it means is settled by
// 004's governing rule: a mechanism that failed must not read as approval. A
// check that ran out of time never answered, exactly like a check that could not
// start, so it refuses for the same reason.
//
// Note the shape of the check: it backgrounds a long sleep rather than sleeping
// itself. That is deliberate, and is the case a naive fix gets wrong — killing
// the shell alone leaves the child alive and holding the inherited pipes, so
// Wait blocks anyway and the deadline achieves nothing.
//
// # RE-VEHICLED onto the NEW gate nature (was old GUARDRAIL.md hooks)
//
// The per-check timeout and the process-group kill are the new check-runner's own
// (internal/dispatch/exec.go: defaultCheckTimeout, 600s in production — every test
// in this file lowers it via SetCheckTimeout so the mechanism is proven without
// each assertion actually waiting out 600 real seconds; runShell runs each check
// in its own process group so a wedged descendant is killed as a group).
// The vehicle is a gate on the pre-write event; the deadline and its message
// reach the agent through the same fail-closed path.
//
// # Why the outer hooks.json timeout matters here too
//
// Claude Code (and a10n-claude-mock, its e2e test double) kills a hook's whole
// PreToolUse invocation at ITS OWN timeout — marketplace/plugins/sloprail/
// hooks/hooks.json now sets that to 300s explicitly (the mock otherwise defaults
// an unset hook to 60s, which — while sloprail's production default is ALSO now
// 60s, chosen to match rather than risk this exact ordering again — was
// discovered by briefly raising sloprail's bound to 90s with hooks.json silent
// on `timeout`: the mock killed the engine before its own 90s deadline could
// fire, so a wedged check's write went through unjudged — exactly the "harness
// kills the engine, which renders no verdict" failure this file's doc above
// warns about). With SetCheckTimeout lowering sloprail's bound to a few seconds
// for this test, 300s leaves enormous margin regardless.
const testCheckTimeout = "4s"

func TestT019_09_AnExpiredHookIsKilledAndRefuses(t *testing.T) {
	e := New(t)
	e.SetCheckTimeout(testCheckTimeout)
	proj := e.Project()
	e.Gate(proj, "wedged", bindEveryWrite, map[string]string{
		// Never returns on its own: longer than any deadline in play, so
		// finishing is never an explanation for the run ending.
		"h.sh": "#!/bin/sh\ncat >/dev/null\nsleep 600 &\nwait\n",
	})

	start := time.Now()
	res := e.Run(proj, "s-019-09", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))
	elapsed := time.Since(start)

	// The bound is the ENGINE's (testCheckTimeout), not the harness's outer
	// hooks.json timeout (300s). The harness would also stop this run
	// eventually but with no verdict — so a run that only ends is not
	// evidence. Ending well inside the harness's bound is what says sloprail's
	// deadline is the one that fired.
	require.Less(t, elapsed, 60*time.Second,
		"the run outlasted the engine's own deadline, so it was the harness that ended it — and a killed engine renders no verdict: %s", elapsed)

	assert.True(t, res.Refused(),
		"a check that ran out of time was read as approval — the mechanism failing must not permit:\n%s", res.Output)
	assert.False(t, e.Exists(proj, "notes.md"),
		"the work went through while no rule had judged it — this is the fail-open")
	assert.True(t, res.Saw("wedged"),
		"the refusal must name the guardrail that timed out, or the author cannot tell which rule wedged")
}

// T019_09b: the expiry refusal says the check ran out of TIME, and how long it
// was given.
//
// Asserted separately from the refusal itself because "it refuses" and "the
// author can act on it" are different properties, and the second is the one
// that decides whether a wedged rule ever gets fixed. A timeout is invisible
// from the outside: the script is present, executable, and correct-looking, and
// nothing about the run says which of the project's rules stopped answering.
//
// It must NOT say "exit -1". A signal-killed process has no exit status of its
// own; ExitCode() == -1 is Go's sentinel for "died by signal", and printing it
// sends the author to debug an exit path that was never taken.
func TestT019_09b_TheExpiryRefusalSaysWhatHappened(t *testing.T) {
	e := New(t)
	e.SetCheckTimeout(testCheckTimeout)
	proj := e.Project()
	e.Gate(proj, "silent-wedge", bindEveryWrite, map[string]string{
		"h.sh": "#!/bin/sh\ncat >/dev/null\nsleep 600 &\nwait\n",
	})

	res := e.Run(proj, "s-019-09b", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	require.True(t, res.Refused(), "an expired check must refuse")
	assert.False(t, res.Saw("exit -1"),
		"the reason reports a status no process can return, sending the author to debug an exit path never taken:\n%s", res.Output)
	assert.True(t, res.Saw("killed"),
		"the reason must say the check was killed rather than that it decided:\n%s", res.Output)
	assert.True(t, res.Saw(testCheckTimeout),
		"the reason must say how long the check was given, or the author cannot tell a wedged rule from a merely slow one:\n%s", res.Output)
}

// T019_09d: the kill reaches what the check SPAWNED, not just the check.
//
// The half of the deadline that a verdict cannot show. Bounding the wait and
// killing the tree are separate achievements: stop waiting on a wedged check and
// the engine answers on time, correctly, while the subprocess it launched runs
// on — now orphaned, outliving the session that started it, and invisible to
// everything. Every other assertion in this file passes in that state, which is
// exactly why this one is needed: an engine that leaks a model-calling process
// per guarded action has a defect that no verdict reveals.
//
// It is a real shape, not a contrived one. A check that shells out to sr-agent
// makes that agent a child of the check's shell, so a kill aimed at the shell
// alone leaves the expensive thing running. Killing the process group is what
// reaches it.
//
// The probe: the check spawns a descendant that waits out the deadline and then
// writes a file. If the group kill worked, that descendant is dead long before
// it writes and the file never appears. If only the shell was signalled, it
// survives and leaves its mark — so the file's existence IS the leak.
func TestT019_09d_TheKillReachesTheHooksDescendants(t *testing.T) {
	e := New(t)
	e.SetCheckTimeout(testCheckTimeout)
	proj := e.Project()
	e.Gate(proj, "spawner", bindEveryWrite, map[string]string{
		// The descendant sleeps past the engine's deadline, then reports that
		// it outlived it. The shell then wedges so the deadline is what ends
		// this, rather than the check returning on its own.
		"h.sh": "#!/bin/sh\ncat >/dev/null\n(sleep 7; echo leaked > \"$SR_GUARDRAIL_DIR/leaked\") &\nsleep 600 &\nwait\n",
	})

	res := e.Run(proj, "s-019-09d", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))
	require.True(t, res.Refused(), "the expired check must still refuse")

	// Outlast the descendant's own timer. Until it has had the chance to write,
	// its silence proves nothing. e.Run already blocked until the engine's
	// testCheckTimeout deadline killed the check; this covers the remaining
	// gap to the descendant's own 7s sleep, plus margin.
	time.Sleep(5 * time.Second)

	assert.Empty(t, e.GateLedgerLines(proj, "spawner", "leaked"),
		"a process the check spawned outlived the kill: the signal reached the shell but not its children, so every guarded action leaks a subprocess")
}

// T019_09c: a slow check that answers INSIDE the deadline is still obeyed.
//
// The control the deadline needs. A check may legitimately be a model call —
// the sr-agent path is exactly that — and the failure this guards against is a
// bound so eager that a judge gets cut off mid-answer: the rule then works on
// an idle machine and refuses on a loaded one, which is worse than having no
// rule, because it is a refusal nobody can reproduce.
//
// It asserts the check's own verdict governed, not merely that something
// refused. An engine that killed every slow check would also "refuse" here, and
// only the check's distinctive text tells the two apart.
func TestT019_09c_ASlowHookInsideTheDeadlineStillDecides(t *testing.T) {
	e := New(t)
	e.SetCheckTimeout(testCheckTimeout)
	proj := e.Project()
	e.Gate(proj, "deliberate", bindEveryWrite, map[string]string{
		"h.sh": "#!/bin/sh\ncat >/dev/null\nsleep 1\necho '{\"reason\":\"the slow rule thought about it and said no\"}'\nexit 1\n",
	})

	res := e.Run(proj, "s-019-09c", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	assert.True(t, res.Saw("the slow rule thought about it and said no"),
		"a check well inside the deadline must run to completion and its own words must govern:\n%s", res.Output)
	assert.False(t, res.Saw("killed"),
		"a check that answered in time must not be reported as killed:\n%s", res.Output)
	assert.True(t, res.Refused(), "and its refusal must stand")
	assert.False(t, e.Exists(proj, "notes.md"), "and prevent the work")
}
