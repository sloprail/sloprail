package e2e

import "testing"

// A gate whose check dies by a signal it did not survive.
//
// The check reads its payload, then sends SIGKILL to its own shell (`kill -9
// $$`) — standing in for any violent death (a crash, an OOM kill, an outside
// `kill`). It never reaches a clean exit and never times out; it dies in
// milliseconds. A PreFileWrite gate, so it runs at pre-tool, where the refusal reaches the
// agent's stream and its wording can be read back.
const crasherGuard = `on:
  - event: PreFileWrite
    match: event.path startsWith "notes/"
checks:
  - script: ./crash.sh
`

const crashScript = `#!/bin/sh
cat >/dev/null
kill -9 $$
`

// T036_01: a check killed by a signal says it was KILLED, not "exit -1".
//
// The sibling of the old-format 019_09e on the new path. 034 already pins that a
// check that cannot reach a verdict refuses (fail-closed); what this pins is what
// the author is TOLD. The regressed answer was "the check refused (exit -1) but
// gave no reason" — ExitCode() == -1 is Go's sentinel for died-by-signal, encoded
// as a number that means the opposite, sending the author to look for the bug in
// the check's exit path, the one place it is not.
//
// Distinct from the timeout message deliberately: the engine must not claim a
// deadline it did not impose. This check died at once, so the reason must say
// "killed" without a duration and without the "did not answer in time" wording
// the timeout path uses. Recovered from runShell's WaitStatus.Signal(), which
// carries the real signal that ExitCode() flattens away.
func TestT036_01_ACrashedCheckSaysItWasKilledNotExitMinusOne(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "crasher", crasherGuard, map[string]string{"crash.sh": crashScript})

	res := e.Run(proj, "s-036-01", "write a note", Turns("done",
		Write("w1", "notes/first.md", "hello"),
	))

	// Fail-closed is preserved: a check the OS killed must still refuse the write.
	if !res.Refused() {
		t.Fatalf("a check killed by a signal must refuse the write (fail-closed):\n%s", res.Output)
	}
	if e.Exists(proj, "notes/first.md") {
		t.Errorf("the write LANDED despite the killed check refusing it before it landed:\n%s", res.Output)
	}

	// The regressed message reported a status no process can return, sending the
	// author to debug an exit path never taken.
	if res.Saw("exit -1") {
		t.Errorf("the reason still reports \"exit -1\" — a status no process returns:\n%s", res.Output)
	}
	// The message must say the check was killed rather than that it decided.
	if !res.Saw("killed") {
		t.Errorf("the reason must say the check was killed, not that it decided:\n%s", res.Output)
	}
}
