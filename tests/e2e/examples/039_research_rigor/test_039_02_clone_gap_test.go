package e2e

import (
	"strings"
	"testing"
)

// T039_04: even a research run that DID `git clone` and DID run `gh` searches is
// refused with "No git clone found" — the shipped clone check reads the clone's
// output off the wrong transcript entry, so it never sees the clone.
//
// This PINS the blocking example bug (verify-depth.sh check 1): it selects the
// entry carrying the clone's PreCommandInvoke event (the assistant tool_use
// record) and reads its `.toolUseResult`, but toolUseResult lives on the
// tool_RESULT record, not the tool_use one. The extracted value is therefore
// always empty and the check refuses regardless of whether a clone ran — so the
// depth gate's PASS path and every later check (page count, sub-agent isolation)
// are unreachable. Not a mock limitation: the jq reads null the same way against
// a real session's transcript.
//
// The turn declares #research atomically with the `git clone` (a SayBash combined
// turn — a pure-text tag turn would be terminal and the clone would never run),
// then runs a `gh` search with --limit 5. Despite both, the gate refuses on the
// clone check.
//
// If verify-depth.sh is later fixed to detect the clone from the invocation (or
// the toolUseResult is read off the result entry), this run would advance past
// check 1 — and THIS test's expectation must change. The failure will point here.
func TestT039_04_CloneNotDetected_BlockingBug(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-039-04"
	res := e.Run(proj, sess, "deep research", Turns("done",
		// #research declared atomically with a real git clone invocation.
		SayBash("b1", "Cloning to study it. #research", "git clone https://github.com/owner/repo /tmp/study"),
		// A gh search covering >=5 pages (would satisfy check 2 if it were reached).
		Bash("b2", "gh search repos guardrail llm agent --limit 5"),
	))

	// The context activated (the tag is present).
	if active, _ := e.ContextState(proj, sess, "research-run"); !active {
		t.Fatalf("the research context did not activate on #research")
	}
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	joined := strings.Join(blocks, "\n")
	if !strings.Contains(joined, noCloneReason) {
		t.Fatalf("EXPECTED the depth gate to STILL refuse on 'No git clone found' despite a real "+
			"clone (check 1 reads toolUseResult off the wrong entry). It did not — verify-depth.sh's "+
			"clone detection may have been fixed; if so, update this test to drive the pass path.\n"+
			"blocks=%v\noutput=%s", blocks, res.Output)
	}
}
