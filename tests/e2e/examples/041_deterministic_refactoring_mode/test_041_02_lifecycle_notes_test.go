package e2e

import (
	"testing"
)

// This file PINS two observed behaviors of the shipped deterministic-refactoring
// example that differ from the design its comments describe, so the suite is
// honest about what the lifted files do. Neither stops the file-guard's core
// reconciliation checks (T041_01..05) from working; both concern the CONTEXT half.

// T041_06: the `refactoring` context activates on ANY PreToolUse, not only when a
// `#refactor` was declared — so the guard fires on a moved-from marker even with
// NO refactor declaration.
//
// enter.sh means to DECLINE (not activate) when it finds no #refactor, and does so
// with `exit 0` and no stdout. But the engine's enter semantics read "clean exit,
// empty stdout" as "ACTIVATE, keeping the prior payload" (dispatch/context.go) —
// the way to decline is a NON-ZERO exit or an unmet `require`, per the enter-
// semantics tests. So enter.sh's exit-0 decline is a no-op: the context activates
// on every PreToolUse. The practical effect is benign for THIS guard (its match
// also requires an sr:moved-from marker, which is the real narrowing — see
// T041_04), but the "only inside a declared refactor" intent is not achieved.
//
// This test asserts the CURRENT behavior: a moved-from write with NO #refactor
// prose is STILL guarded (refused when it does not reconcile).
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
}

// T041_07: the context's exit.sh "declared markers were never written"
// completeness refusal does NOT fire — a GENUINELY SEPARATE gap, deliberately
// still pinned, distinct from the --owner cross-guardrail slice that fixed the
// other composites. deterministic-refactoring's LOAD-BEARING check is the
// preventive file-guard (T041_01..05), which enforces correctly; this exit.sh
// completeness check is a secondary "did every declared move actually land" pass
// with three independent problems, none of which the --owner work touches:
//
//  1. enter.sh matches the #refactor tag with `any(.tags[]?; ...)` on the
//     normalized event, but tags live under `.fields.tags` — so the scope is never
//     read and the payload carries no declared_markers (the same wire-shape bug the
//     depth/coverage gates had; here it is in a context enter this slice did not
//     cover).
//  2. exit.sh greps `grep -r "sr:$marker"`, but $marker already begins `sr:`
//     (the scope list keeps each element's prefix), so it searches for
//     `sr:sr:moved-from:...` — a double prefix nothing matches.
//  3. the declared scope token (`sr:moved-from:beta`, a logical name) has no
//     defined correspondence to the file marker syntax a move actually writes
//     (`sr:moved-from <path>@<sha>:<lines>`), so even a de-doubled grep would not
//     know what to look for. This is a DESIGN gap in the example, not a one-line
//     fix, which is why it is left pinned rather than patched here.
//
// This test documents the current outcome: a declared-but-unwritten scope produces
// NO Stop block. If the example's completeness half is redesigned so exit.sh fires,
// this test's expectation flips and should be updated to assert the block.
func TestT041_07_ExitCompletenessDoesNotFire_Gap(t *testing.T) {
	env, _ := setupOrigin(t)
	e, proj := env.e, env.proj

	sess := "s-041-07"
	// Declare a scope marker but write NOTHING that carries it.
	res := e.Run(proj, sess, "declare a move but never make it", Turns("done",
		SayWrite("w1", "Refactoring. #refactor scope=sr:moved-from:zeta", "unrelated.md", "nothing to do with any marker"),
	))
	_ = res

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	for _, b := range blocks {
		if containsAny(b, "never written", "not complete") {
			t.Fatalf("exit.sh's completeness refusal FIRED (unexpected through this harness): %q — "+
				"the enter/exit interaction may have been fixed; update this test to assert the block", b)
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
