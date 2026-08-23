package e2e

import (
	"testing"
)

// This file covers the CONTEXT + STOP-GATE half of the deterministic-refactoring
// example — the "did every declared move actually land" completeness check. In
// the new nature format a context's exit is PURE LIFECYCLE and cannot block a
// Stop (only a gate blocks), so the completeness refusal lives in the paired Stop
// gate `gate/refactor-complete`, not in the context's exit. These tests prove the
// gate blocks an incomplete refactor (T041_07) and permits a completed one
// (T041_08), and pin the context-activation behavior the file-guard rests on
// (T041_06). None of this disturbs the preventive file-guard (T041_01..05).

// T041_06: the `refactoring` context activates on ANY PreToolUse, not only when a
// `#refactor` was declared — so the file-guard fires on a moved-from marker even
// with NO refactor declaration.
//
// enter.sh means to DECLINE (not activate) when it finds no #refactor, and does so
// with `exit 0` and no stdout. But the engine's enter semantics read "clean exit,
// empty stdout" as "ACTIVATE, keeping the prior payload" (dispatch/context.go) —
// the way to decline is a NON-ZERO exit or an unmet `require`. So enter.sh's
// exit-0 decline is a no-op: the context activates on every PreToolUse. The
// practical effect is benign for the file-guard (its match ALSO requires an
// sr:moved-from marker, which is the real narrowing — see T041_04), and benign for
// the completeness gate (with no #refactor declared, declared_markers is empty and
// the gate permits — proven here: this run carries no Stop completeness block).
//
// This test asserts the CURRENT behavior: a moved-from write with NO #refactor
// prose is STILL guarded (refused when it does not reconcile), and no completeness
// block fires (nothing was declared).
func TestT041_06_ContextActivatesWithoutRefactorDeclaration(t *testing.T) {
	env, sha := setupOrigin(t)
	e, proj := env.e, env.proj

	sess := "s-041-06"
	// A non-reconciling moved-from write, using a PLAIN Write (no #refactor prose).
	movedBad := "// sr:moved-from origin.go@" + sha + ":1-3\nfunc Beta() int {\n\treturn 999\n}\n"
	res := e.Run(proj, sess, "write a marked file without declaring a refactor", Turns("done",
		Write("w1", "dest.go", movedBad),
	))

	if !res.Refused() {
		t.Fatalf("EXPECTED the guard to still fire on a moved-from marker with no #refactor "+
			"declared (the context activates on any PreToolUse). It did not — the enter-decline "+
			"semantics may have changed; if so, update this test.\n%s", res.Output)
	}
	if !res.Saw(reconcileRefusal) {
		t.Errorf("refused, but not with the reconcile reason:\n%s", res.Output)
	}
	// No refactor was DECLARED, so the completeness gate has nothing to complete and
	// must NOT block the Stop (empty declared_markers → permit).
	for _, b := range e.BlockingErrorsFrom(proj, sess, "Stop") {
		if containsAny(b, completenessRefusal, "never landed", "not complete") {
			t.Errorf("the completeness gate blocked a turn that declared no refactor: %q\n%s", b, res.Output)
		}
	}
}

// T041_07: a DECLARED refactor whose moves were NEVER WRITTEN is BLOCKED at Stop
// by the completeness gate.
//
// This is the fix's core assertion. The blocking is done by the Stop GATE
// `gate/refactor-complete`, NOT by the context's exit (a context's exit is pure
// lifecycle and cannot refuse a Stop — services/sr-session/nature_context.go).
// The gate reads the refactoring context's `declared_markers` (populated by the
// context's enter at the PostTagWrite trigger, which fires at Stop once the turn
// is settled in the transcript) and refuses when a declared move never landed as
// an `sr:moved-from` marker.
//
// The declared scope names the fqn a completed move WOULD carry
// (`origin.go@<sha>:1-3`); the turn writes an unrelated file carrying no such
// marker, so the move is outstanding and the turn cannot end.
//
// (This test previously PINNED the broken state — it asserted NO Stop block,
// because three bugs made the old context-exit completeness check dead: (1) enter
// read the #refactor tag off `.tags` instead of `.fields.tags`, so the scope was
// never captured; (2) the exit greped `sr:$marker` where $marker already began
// `sr:`, a double prefix; (3) the declared token was a logical nickname with no
// correspondence to the landed-marker syntax. All three are fixed: the tag field
// is read correctly AND the enter now also runs at the PostTagWrite trigger so the
// scope is visible; the gate greps for the literal marker with no double prefix;
// and the declaration names the actual fqn, so a declared token IS a landed
// marker's fqn. The test is flipped to assert the block those fixes produce.)
func TestT041_07_IncompleteRefactorBlocksAtStop(t *testing.T) {
	env, sha := setupOrigin(t)
	e, proj := env.e, env.proj

	sess := "s-041-07"
	// Declare a move (by its fqn) but write NOTHING that carries that marker.
	res := e.Run(proj, sess, "declare a move but never make it", Turns("done",
		SayWrite("w1", declRefactor(sha), "unrelated.md", "nothing to do with any marker"),
	))

	// The completeness gate must refuse the Stop, naming the incomplete refactor.
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	sawCompleteness := false
	for _, b := range blocks {
		if containsAny(b, completenessRefusal, "never landed") {
			sawCompleteness = true
		}
	}
	if !sawCompleteness {
		t.Fatalf("EXPECTED the completeness gate to block the Stop for a declared-but-unwritten "+
			"refactor, naming it. Stop blocks seen: %v\n%s", blocks, res.Output)
	}
	// The unrelated write itself carries no moved-from marker, so the preventive
	// file-guard leaves it alone — the block is the GATE's, not the file-guard's.
	if res.Refused() {
		t.Errorf("the unrelated write was refused at PreToolUse; it carries no moved-from marker "+
			"and should pass the file-guard — the completeness block belongs at Stop, not here:\n%s", res.Output)
	}
}

// T041_08: a DECLARED refactor whose move ACTUALLY LANDS (and reconciles) is
// PERMITTED — the completeness gate is not a blanket "always block", it passes
// once the declared move is present.
//
// The agent declares scope=origin.go@<sha>:1-3 AND writes dest.go carrying
// `sr:moved-from origin.go@<sha>:1-3` with the exact origin bytes. The preventive
// file-guard admits the reconciling write, it lands, and at Stop the completeness
// gate finds the declared move's marker in the tree and permits — no Stop block.
func TestT041_08_CompletedRefactorPermitsAtStop(t *testing.T) {
	env, sha := setupOrigin(t)
	e, proj := env.e, env.proj

	sess := "s-041-08"
	moved := "// sr:moved-from origin.go@" + sha + ":1-3\nfunc Beta() int {\n\treturn 1\n}\n"
	res := e.Run(proj, sess, "declare and complete the move", Turns("done",
		SayWrite("w1", declRefactor(sha), "dest.go", moved),
	))

	// The reconciling move landed — the preventive file-guard admitted it.
	if res.Refused() {
		t.Fatalf("the reconciling declared move was refused at PreToolUse:\n%s", res.Output)
	}
	if !e.Exists(proj, "dest.go") {
		t.Fatalf("the declared move did not land")
	}
	// And the completeness gate PERMITS at Stop: the declared move is present, so no
	// Stop completeness block fires. This is what proves the gate isn't just always
	// blocking.
	for _, b := range e.BlockingErrorsFrom(proj, sess, "Stop") {
		if containsAny(b, completenessRefusal, "never landed", "not complete") {
			t.Fatalf("the completeness gate blocked a COMPLETED refactor: %q\n%s", b, res.Output)
		}
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && indexOf(s, sub) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
