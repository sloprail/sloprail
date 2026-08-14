package main

import (
	"strings"
	"testing"
)

// A hook killed by a signal is reported as KILLED, whichever way the platform's
// /bin/sh chose to tell us about it.
//
// The behaviour being pinned is one fact about the world — the hook did not
// exit, it was killed — and the author has to be told that fact. What differs
// between platforms is only the SHAPE the report arrives in, because a hook is
// run as `sh -c <command>` and /bin/sh is a different program on each:
//
//	macOS  /bin/sh is bash: it re-raises the signal, so the direct child dies by
//	       signal and Go reports ExitCode() == -1 with both streams empty.
//	Linux  /bin/sh is dash: it OUTLIVES the killed command and reports on it,
//	       exiting 128+N (137 for SIGKILL) with "Killed\n" on stderr.
//
// Both shapes are measured, not supposed. On ubuntu-24.04 a self-SIGKILLing
// script run as `sh -c` gave code=137 stderr="Killed\n" under dash and code=-1
// stderr="" under bash on the same machine; on macOS every shell gave code=-1
// with stderr empty.
//
// This is a unit test rather than only an e2e because an e2e can only ever
// exercise whichever shell the HOST happens to have. The e2e that covers this
// (T019_09e) passed on macOS and failed on Linux for exactly that reason: it
// was pinning one platform's shape while believing it pinned the behaviour.
// Driving the function directly is what lets both shapes be asserted from
// either machine.
func TestRefusalReason_AKilledHookIsReportedAsKilledOnEitherShellsReport(t *testing.T) {
	for _, tc := range []struct {
		name   string
		code   int
		stderr []byte
	}{
		// The shell died with the command. Go's sentinel for "no exit status".
		{"the shell re-raised the signal (bash, macOS)", -1, nil},

		// The shell survived and reported. 128+N, with its own obituary on
		// stderr — the line that was previously read as the RULE speaking.
		{"the shell reported its killed child (dash, Linux)", 137, []byte("Killed\n")},

		// The same convention for the other deaths a hook really suffers.
		{"segfault", 139, []byte("Segmentation fault\n")},
		{"abort", 134, []byte("Aborted\n")},
		{"terminated", 143, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := refusalReason("./crash.sh", tc.code, nil, tc.stderr)
			if !strings.Contains(got, "killed") {
				t.Fatalf("a hook that was killed must be reported as killed, or the author goes "+
					"looking for the bug in an exit path the hook never took. Got: %q", got)
			}
			// And it must not report a number that means nothing. "exit -1" is
			// not a status any process can return.
			if strings.Contains(got, "exit -1") {
				t.Fatalf("the reason reports a status no process can return: %q", got)
			}
		})
	}
}

// A hook that CHOSE to exit with a high status is still the rule speaking, and
// must not be relabelled as a crash.
//
// The complement of the test above, and the reason killedBySignal lists
// specific signals rather than treating everything over 128 as a death. Codes
// in that range are ordinary exit statuses a script may return on its own
// account; reading them all as signals would tell an author their rule crashed
// when it had in fact decided, which is the same confusion in the opposite
// direction and the more dangerous one — it discards a real verdict's words.
func TestRefusalReason_AHookThatChoseAHighExitStatusStillSpeaksForItself(t *testing.T) {
	// 129 and 140 are in the 128+N range but are not signals this reads as a
	// death, so the hook's own words must survive.
	for _, code := range []int{1, 2, 129, 140, 141, 200} {
		got := refusalReason("./rule.sh", code, nil, []byte("this file needs a test\n"))
		if got != "this file needs a test" {
			t.Fatalf("a hook that exited %d with a reason must have that reason reported, not be "+
				"relabelled as a crash. Got: %q", code, got)
		}
	}
}

// Whatever the shell said is carried along, so the diagnosis does not hide it.
//
// The stderr that made this bug possible is still worth showing — it just must
// not be presented as the rule's own verdict. It arrives parenthesised, the
// same way the could-not-run diagnoses carry the shell's message.
func TestRefusalReason_TheShellsOwnWordsSurviveTheDiagnosis(t *testing.T) {
	got := refusalReason("./crash.sh", 137, nil, []byte("Killed\n"))
	if !strings.Contains(got, "Killed") {
		t.Fatalf("the shell's own message is evidence and must not be dropped: %q", got)
	}
	if !strings.Contains(got, "killed before it answered") {
		t.Fatalf("but the engine's diagnosis is what leads: %q", got)
	}
}
