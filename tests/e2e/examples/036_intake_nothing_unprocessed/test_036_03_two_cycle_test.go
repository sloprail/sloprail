package e2e

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// T036_06: a skip DECLARED IN A LATER CYCLE still excuses an EARLIER cycle's
// message — the skip-declared context's registry (sr-session state) persists
// across Stop cycles even though the context's OWN active flag does not
// (exit.sh unconditionally deactivates every Stop: "the skips live in state
// that persists across cycles on its own").
//
// Every existing test in this suite drives exactly one Stop cycle. T036_01
// proves an unaccounted message is refused WITHIN that cycle; T036_04 proves a
// #skip declared in the SAME cycle as the message excuses it. Neither proves
// the doc comment's actual cross-cycle claim: that the skip REGISTRY, not the
// context's active flag, is what survives — because verify-no-residue.sh reads
// `state list --owner skip-declared` fresh every Stop, independent of whether
// skip-declared happens to be active THIS cycle.
//
// Cycle 1: the one user message (the root prompt) is left unaccounted —
// refused, same as T036_01. Cycle 2: the agent declares #skip naming that
// FIRST message's line (not a new message) — no task, no new unaccounted
// message, just the retroactive skip. The residue must empty and the Stop
// must admit, proving the skip resolves an ALREADY-OPEN violation from an
// earlier cycle, not merely one declared inline with the message itself.
func TestT036_06_SkipDeclaredLaterCycleExcusesEarlierMessage(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install example")

	sess := "s-036-06"

	// ---- Cycle 1: the root message is left unaccounted — refused. ----
	res1 := e.Run(proj, sess, "please handle request A", Turns("done",
		Say("m1", "I looked at it but recorded nothing yet."),
	))
	if len(e.BlockingErrorsFrom(proj, sess, "Stop")) == 0 {
		t.Fatalf("cycle 1: the intake gate did not refuse the unaccounted message — "+
			"the two-cycle assertion below would be meaningless if this did not hold first:\n%s", res1.Output)
	}
	line := e.RootMessageLine(sess)

	// Cycle 2's own resumed prompt ALSO lands as a new accountable user message
	// (--resume appends the prompt as a continuation human turn — harness.go's
	// own doc on this). Its line is not known up front the way the root's is, so
	// it is read from the transcript the mock already wrote for cycle 1: the
	// next line appended is one past the file's current line count.
	nextLine := transcriptLineCount(t, e.TranscriptPath(proj, sess)) + 1

	// ---- Cycle 2: skip the FIRST message's line AND this cycle's own resumed
	// prompt, declaring no new work of its own. ----
	res2 := e.Run(proj, sess, "just skipping the earlier request now", Turns("done",
		Say("m2", "On reflection neither message needs a task. #skip "+strconv.Itoa(line)+" "+strconv.Itoa(nextLine)),
	))

	reg := e.GuardrailState(proj, sess, "skip-declared", "skip:")
	skipSuffix := ":" + strconv.Itoa(line) + "-" + strconv.Itoa(line)
	if !anyKeyHasSuffix(reg, skipSuffix) {
		t.Fatalf("cycle 2: the skip-declared context did not log a skip for the first message's line (%s); registry keys=%v", skipSuffix, keysOf(reg))
	}
	// GateState, not BlockingErrorsFrom: BlockingErrorsFrom de-dupes by text and
	// ACCUMULATES across cycles, so cycle 1's identical-text refusal is still
	// on record here even though cycle 2 genuinely passed — GateState is the
	// LAST verdict this cycle, the one that actually answers "did skipping fix it".
	if status := e.GateState(proj, sess, "verify-intake-complete"); status != "pass" {
		t.Fatalf("cycle 2: the verify-intake-complete gate recorded %q after the earlier message was retroactively skipped, want pass:\n%s", status, res2.Output)
	}
	if res2.Refused() {
		t.Errorf("cycle 2: unexpected refusal:\n%s", res2.Output)
	}
}

// transcriptLineCount reads how many lines the transcript currently holds, so a
// test can predict the physical line a --resume's appended prompt will land on
// BEFORE that append happens (it lands on count+1).
func transcriptLineCount(t *testing.T, path string) int {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read transcript %s: %v", path, err)
	}
	return strings.Count(string(body), "\n")
}
