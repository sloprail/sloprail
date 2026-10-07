package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T050_03/04: the `recording` context (README's "Part 4") plus its paired
// `recording-verify` gate had ZERO e2e coverage before this file, and the gate
// did not exist at all until this fix. Independent of goal-tracking/goal-verify
// (T050_01/02 above): `recording` wakes on any `PreCommandInvoke` whose `.bin` is
// `eval`, re-enters per invocation, and `recording-verify` (bound to Stop,
// require:[{context: recording}]) refuses the Stop unless every eval run this
// trajectory produced has a markdown file referencing the run path the command
// printed on its own stdout.
//
// Before this fix, `context/recording/exit.sh` tried to refuse the Stop directly
// (a non-zero exit carrying a {"decision":"block",…} body) — the OLD context
// contract. Under the current engine (services/sr-session/nature_context.go's
// runContextExits: "exit is pure lifecycle... nothing here contributes to a turn
// block"), a context's exit verdict ONLY flips `active`; its stdout is never read
// as a refusal. So an undocumented eval run never actually blocked a Stop,
// contradicting the README. The fix splits it the same way goal-tracking/
// goal-verify already are: the real check moved to gate/recording-verify/
// run-verify.sh (which DOES have a refusal channel), and exit.sh became a thin
// read of that gate's own verdict — proven by T050_03's use of GateState +
// BlockingErrorsFrom rather than res.Refused() (a Stop-gate block is not surfaced
// through the pre-tool refusal path).
//
// dummy-eval.sh (shipped alongside the context, not part of the guardrail itself)
// is the fixture: it writes evals/runs/<id>.json and prints that path. `eval
// ./.sloprail/context/recording/dummy-eval.sh` is a real command the mock's Bash
// tool genuinely executes (the same real-execution mechanism T021_08 proves for
// `sed -i`) — `eval CMD` is a shell builtin that evaluates CMD as shell code, so
// this both (a) makes commandmod's static AST walk see bin=="eval" (the literal
// word the match/run-verify.sh scan for) and (b) genuinely runs dummy-eval.sh,
// landing its printed run path on the REAL tool_result entry's `.content` field
// the mock's Bash executor produces (NOT `.toolUseResult`, which a genuinely
// executed command never populates — only a synthesized-artifact tool like
// `screenshot` does; run-verify.sh's own fix comment covers this) — no
// ToolUseWithResult stand-in needed here.

const dummyEvalCmd = "eval ./.sloprail/context/recording/dummy-eval.sh"

// T050_03: an eval run with NO markdown documenting it BLOCKS the Stop, naming
// the undocumented run path — and a SECOND cycle, still undocumented, blocks
// AGAIN (the two-Stop-cycle persistence/re-fire proof, the same shape T050_01
// uses for goal-verify).
func TestT050_03_UndocumentedEvalRunBlocksAndRefires(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)

	sess := "s-050-03"

	// ---- Cycle 1: run an eval, stop without writing it up. ----
	res1 := e.Run(proj, sess, "run an eval and stop without writing it up", Turns("done",
		Bash("b1", dummyEvalCmd),
	))
	runPath := findRunPath(t, proj)

	active, _ := e.ContextState(proj, sess, "recording")
	if !active {
		t.Fatalf("the recording context did not activate on a PreCommandInvoke whose .bin is \"eval\":\n%s", res1.Output)
	}
	if status := e.GateState(proj, sess, "recording-verify"); status != "fail" {
		t.Fatalf("the recording-verify gate recorded %q for an undocumented run, want fail", status)
	}
	blocks1 := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks1) == 0 {
		t.Fatalf("an eval run with no markdown documenting it was not refused:\n%s", res1.Output)
	}
	joined1 := strings.Join(blocks1, "\n")
	if !strings.Contains(joined1, "no markdown documenting them") {
		t.Errorf("the recording-verify gate's completeness refusal did not reach the agent:\n%s", joined1)
	}
	if !strings.Contains(joined1, runPath) {
		t.Errorf("the refusal did not name the undocumented run path %q:\n%s", runPath, joined1)
	}
	blocksAfter1 := rawBlockRecords(t, e, proj, sess, runPath)
	if blocksAfter1 == 0 {
		t.Fatalf("no refusal naming %q was recorded on the transcript in cycle 1", runPath)
	}

	// ---- Cycle 2: more unrelated work, STILL undocumented — must block AGAIN. ----
	e.Run(proj, sess, "do something else, still no writeup", Turns("done",
		Write("w2", "notes.txt", "forgot to write up the run"),
	))
	if active, _ := e.ContextState(proj, sess, "recording"); !active {
		t.Fatalf("the recording context deactivated while the run was still undocumented — the gate would then never re-fire")
	}
	if status := e.GateState(proj, sess, "recording-verify"); status != "fail" {
		t.Fatalf("the recording-verify gate recorded %q on the second undocumented Stop, want fail — it did not re-fire", status)
	}
	blocksAfter2 := rawBlockRecords(t, e, proj, sess, runPath)
	if blocksAfter2 <= blocksAfter1 {
		t.Fatalf("the second undocumented Stop did not add a new refusal naming %q (after cycle 1: %d, after cycle 2: %d) — the gate did not re-fire",
			runPath, blocksAfter1, blocksAfter2)
	}
}

// T050_04: once a markdown file references the run path dummy-eval.sh printed,
// the SAME run is no longer undocumented, the gate ADMITS, and the context
// DEACTIVATES — the control for T050_03, and the deactivation half of the
// two-Stop-cycle proof.
func TestT050_04_DocumentedEvalRunAdmitsAndDeactivates(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)

	sess := "s-050-04"
	e.Run(proj, sess, "run an eval, write it up, stop", Turns("done",
		Bash("b1", dummyEvalCmd),
	))
	runPath := findRunPath(t, proj)
	if status := e.GateState(proj, sess, "recording-verify"); status != "fail" {
		t.Fatalf("precondition: the recording-verify gate did not fail on an undocumented run, got %q", status)
	}

	// A second cycle in the SAME session: the agent documents the run this time.
	// The trajectory-scan (sr-session trajectory normalize) reads the WHOLE
	// session's PreCommandInvoke history, so the run from cycle 1 is still what
	// run-verify.sh must find documented in cycle 2.
	res := e.Run(proj, sess, "now write up the run", Turns("done",
		Write("w1", "evals/writeup.md", "Ran the eval; see "+runPath+" for scores."),
	))

	if status := e.GateState(proj, sess, "recording-verify"); status != "pass" {
		t.Fatalf("the recording-verify gate recorded %q after the run was documented, want pass:\n%s", status, res.Output)
	}
	if active, _ := e.ContextState(proj, sess, "recording"); active {
		t.Fatalf("the recording context stayed active after the run was documented — its exit should read the gate's pass and deactivate")
	}
}

// findRunPath reads the ONE run file dummy-eval.sh wrote under evals/runs/ in
// the project — its id is time-and-pid-derived, so the test discovers the real
// path from disk rather than predicting it.
func findRunPath(t *testing.T, proj string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(proj, "evals", "runs"))
	if err != nil {
		t.Fatalf("dummy-eval.sh did not create evals/runs/: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want exactly 1 eval run file, got %d", len(entries))
	}
	return "evals/runs/" + entries[0].Name()
}
