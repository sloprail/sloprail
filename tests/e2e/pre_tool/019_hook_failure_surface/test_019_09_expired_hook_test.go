package e2e

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T019_09: a hook that never returns is killed, and the expiry refuses.
//
// The last failure mode on this surface, and the one that could not be written
// before. Every other test here fails FAST — a bad exit status, a missing file,
// a crash — and the engine sees a finished process. A hook that hangs never
// finishes at all, so nothing in the fail-open discipline fires on its own: no
// failure is ever observed, because no answer is ever given.
//
// There are two nested deadlines here and only one of them is sloprail's, which
// is the whole design point. The HARNESS bounds the engine — a hook is run by a
// harness, so a harness decides how long the thing it launched may take, and
// real Claude Code does exactly that. The ENGINE bounds the guardrail hook,
// because it is the engine that launched THAT. Getting this wrong is not a
// stylistic matter: with no bound of its own the engine waits on the wedged
// hook until the harness kills the ENGINE, and an engine that has been killed
// has rendered no verdict at all — so the tool call proceeds, unjudged. The
// fail-open arrives by way of sloprail never getting to speak. That was the
// measured behaviour before this test: the write landed.
//
// So the engine's deadline must fire FIRST, and what it means is settled by
// 004's governing rule: a mechanism that failed must not read as approval. A
// hook that ran out of time never answered, exactly like a hook that could not
// start, so it refuses for the same reason. The argument the other way — a rule
// that wedges on some input then blocks that input until someone removes it —
// is real, but it argues for removing the rule, not for the engine inventing
// consent nobody gave.
//
// Note the shape of the hook: it backgrounds a long sleep rather than sleeping
// itself. That is deliberate, and is the case a naive fix gets wrong — killing
// the shell alone leaves the child alive and holding the inherited pipes, so
// Wait blocks anyway and the deadline achieves nothing.
func TestT019_09_AnExpiredHookIsKilledAndRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "wedged", bindEveryWrite, map[string]string{
		// Never returns on its own: longer than any deadline in play, so
		// finishing is never an explanation for the run ending.
		"h.sh": "#!/bin/sh\ncat >/dev/null\nsleep 600 &\nwait\n",
	})

	start := time.Now()
	res := e.Run(proj, "s-019-09", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))
	elapsed := time.Since(start)

	// The bound is the ENGINE's, not the harness's. The harness would also stop
	// this run eventually (at its own, longer deadline) but with no verdict —
	// so a run that only ends is not evidence. Ending well inside the harness's
	// bound is what says sloprail's deadline is the one that fired.
	require.Less(t, elapsed, 55*time.Second,
		"the run outlasted the engine's own deadline, so it was the harness that ended it — and a killed engine renders no verdict: %s", elapsed)

	assert.True(t, res.Refused(),
		"a hook that ran out of time was read as approval — the mechanism failing must not permit:\n%s", res.Output)
	assert.False(t, e.Exists(proj, "notes.md"),
		"the work went through while no rule had judged it — this is the fail-open")
	assert.True(t, res.Saw("wedged"),
		"the refusal must name the guardrail that timed out, or the author cannot tell which rule wedged")
}

// T019_09b: the expiry refusal says the hook ran out of TIME, and how long it
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
// sends the author to debug an exit path that was never taken. The engine did
// exactly that before this test.
func TestT019_09b_TheExpiryRefusalSaysWhatHappened(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "silent-wedge", bindEveryWrite, map[string]string{
		"h.sh": "#!/bin/sh\ncat >/dev/null\nsleep 600 &\nwait\n",
	})

	res := e.Run(proj, "s-019-09b", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	require.True(t, res.Refused(), "an expired hook must refuse")
	assert.False(t, res.Saw("exit -1"),
		"the reason reports a status no process can return, sending the author to debug an exit path never taken:\n%s", res.Output)
	assert.True(t, res.Saw("killed"),
		"the reason must say the hook was killed rather than that it decided:\n%s", res.Output)
	assert.True(t, res.Saw("30s"),
		"the reason must say how long the hook was given, or the author cannot tell a wedged rule from a merely slow one:\n%s", res.Output)
}

// T019_09e: a hook killed by a signal it did not survive says so too.
//
// The sibling of 019_09b on the other path. An expired hook is killed by the
// engine, which knows why and says how long it waited. A hook killed by anyone
// ELSE — a segfault, the OOM killer, a stray `kill -9` — reaches the same dead
// end from outside, and the engine has only the corpse to go on.
//
// 019_02 already pins that this refuses. What it never checked is what the
// author is told, and the answer was "refused (exit -1) but gave no reason".
// ExitCode() == -1 is Go's sentinel for "died by signal", not a status any
// process can return, so the one actionable fact — the hook did not exit, it
// was killed — was encoded as a number that means the opposite. An author
// reading it goes looking for the bug in their script's exit path, which is the
// one place it is not.
//
// Distinct from the timeout message deliberately: the engine must not claim a
// deadline it did not impose. This hook died in milliseconds.
func TestT019_09e_ACrashedHookSaysItWasKilledNotExitMinusOne(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "crasher", bindEveryWrite, map[string]string{
		"h.sh": "#!/bin/sh\ncat >/dev/null\nkill -9 $$\n",
	})

	res := e.Run(proj, "s-019-09e", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	require.True(t, res.Refused(), "a hook killed by a signal must refuse")
	assert.False(t, e.Exists(proj, "notes.md"), "and the work must be prevented")
	assert.False(t, res.Saw("exit -1"),
		"the reason reports a status no process can return, sending the author to debug an exit path never taken:\n%s", res.Output)
	assert.True(t, res.Saw("killed"),
		"the reason must say the hook was killed rather than that it decided:\n%s", res.Output)
}

// T019_09d: the kill reaches what the hook SPAWNED, not just the hook.
//
// The half of the deadline that a verdict cannot show. Bounding the wait and
// killing the tree are separate achievements: stop waiting on a wedged hook and
// the engine answers on time, correctly, while the subprocess it launched runs
// on — now orphaned, outliving the session that started it, and invisible to
// everything. Every other assertion in this file passes in that state, which is
// exactly why this one is needed: an engine that leaks a model-calling process
// per guarded action has a defect that no verdict reveals.
//
// It is a real shape, not a contrived one. A hook that shells out to sr-agent
// makes that agent a child of the hook's shell, so a kill aimed at the shell
// alone leaves the expensive thing running. Killing the process group is what
// reaches it.
//
// The probe: the hook spawns a descendant that waits out the deadline and then
// writes a file. If the group kill worked, that descendant is dead long before
// it writes and the file never appears. If only the shell was signalled, it
// survives and leaves its mark — so the file's existence IS the leak.
func TestT019_09d_TheKillReachesTheHooksDescendants(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "spawner", bindEveryWrite, map[string]string{
		// The descendant sleeps past the engine's deadline, then reports that
		// it outlived it. The shell then wedges so the deadline is what ends
		// this, rather than the hook returning on its own.
		"h.sh": "#!/bin/sh\ncat >/dev/null\n(sleep 45; echo leaked > \"$PWD/leaked\") &\nsleep 600 &\nwait\n",
	})

	res := e.Run(proj, "s-019-09d", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))
	require.True(t, res.Refused(), "the expired hook must still refuse")

	// Outlast the descendant's own timer. Until it has had the chance to write,
	// its silence proves nothing.
	time.Sleep(20 * time.Second)

	assert.Empty(t, e.Ledger(proj, "spawner", "leaked"),
		"a process the hook spawned outlived the kill: the signal reached the shell but not its children, so every guarded action leaks a subprocess")
}

// T019_09c: a slow hook that answers INSIDE the deadline is still obeyed.
//
// The control the deadline needs, and the reason the number is 30s rather than
// something tidy. A hook may legitimately be a model call — the sr-agent path
// is exactly that — and the failure this guards against is a bound so eager
// that a judge gets cut off mid-answer: the rule then works on an idle machine
// and refuses on a loaded one, which is worse than having no rule, because it
// is a refusal nobody can reproduce.
//
// It asserts the hook's own verdict governed, not merely that something
// refused. An engine that killed every slow hook would also "refuse" here, and
// only the hook's distinctive text tells the two apart.
func TestT019_09c_ASlowHookInsideTheDeadlineStillDecides(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "deliberate", bindEveryWrite, map[string]string{
		"h.sh": "#!/bin/sh\ncat >/dev/null\nsleep 3\necho 'the slow rule thought about it and said no' >&2\nexit 1\n",
	})

	res := e.Run(proj, "s-019-09c", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	assert.True(t, res.Saw("the slow rule thought about it and said no"),
		"a hook well inside the deadline must run to completion and its own words must govern:\n%s", res.Output)
	assert.False(t, res.Saw("killed"),
		"a hook that answered in time must not be reported as killed:\n%s", res.Output)
	assert.True(t, res.Refused(), "and its refusal must stand")
	assert.False(t, e.Exists(proj, "notes.md"), "and prevent the work")
}
