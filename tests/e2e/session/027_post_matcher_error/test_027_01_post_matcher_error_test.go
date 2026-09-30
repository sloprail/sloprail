// Package e2e asks the matcher half of "a mechanism that fails must not read as
// approval" at the hook point that ends a cycle, in the NEW nature format.
//
// 014 establishes it at the pre-tool point: a gate trigger `match` that cannot be
// EVALUATED is the engine unable to answer whether a rule applies, which is not the
// same as the rule being satisfied, so the action is refused. The engine's own
// comment there spells out the reasoning and calls skipping the binding the defect
// it was corrected for.
//
// The end-of-cycle side has two channels in the new format, and both had the same
// regression:
//
//   - a COMMAND event is a GATE trigger (T027_01, the pre half — restated here so
//     the comparison is against a live build);
//   - a POST FILE event is a FILE-GUARD after-check (T027_02..04). A file-guard's
//     `match` erroring at evaluation was reported to a stderr channel that reaches
//     nobody at Stop and the guard was skipped — so a not-fine file whose guard
//     could not decide was read as fine.
//
// # What this suite now claims
//
// The old 027 pinned an ASYMMETRY: the pre point refused, the post point skipped,
// and T027_02 recorded the skip as a deliberate characterisation while saying it
// was very likely wrong. This is the update it asked for. The post side now
// REFUSES too — a matcher that cannot be evaluated has not answered "this rule does
// not apply", it has not answered at all, and treating the two alike is the same
// mistake as a check exiting 0 because it could not run.
//
// What still differs between the two hook points is the CHANNEL, not the verdict:
// the pre point denies the action outright; the post point holds the cycle and the
// reason arrives as a BLOCKING ERROR at Stop, because a Post event is reported after
// the change is already on disk.
//
// # Why an evaluation error is still reachable
//
// The gate side rides `int(.bin) > 0` on a command (see 014). The file-guard side
// rides the file MATCH scope, where `path` is a bare string: `int(path) > 0`
// COMPILES (int of a string is well-formed in the expression language) and the vm
// then refuses "notes.md" at run time. It is an odd rule to write, and that is the
// honest position — on the flat file scope the evaluation branch is a narrow edge,
// and this suite pins it rather than an everyday mistake. Verified compile-clean-
// but-eval-error against CompileFileMatch before this suite was written; every
// well-typed shape (`path endsWith ".md"`) answers cleanly instead.
package e2e

import (
	"strings"
	"testing"
)

// gateErroringMatcher is a gate whose trigger match cannot be evaluated against the
// command it is bound to.
//
// `int(.bin)` compiles — int of a string is well-formed — and the vm refuses
// "npm" at run time: a rule that LOADS cleanly and fails at EVALUATION, the only
// way to reach the evaluation branch at all.
const gateErroringMatcher = `on:
  - event: PreCommandInvoke
    match: any(event.invocations, int(.bin) > 0)
checks:
  - script: ./check.sh
`

// soundPostGuard is a file-guard whose match can always be answered — the control
// that shows a Post-side file-guard fires at all in this project.
const soundPostGuard = `match: path endsWith ".md"
checks:
  - script: ./check.sh
`

// postErroringGuard is a file-guard whose match compiles and then cannot be
// evaluated on the settled file.
//
// `int(path)` is the one shape that gets through the file MATCH scope: the checker
// accepts the conversion because `int` of a string is well-formed, and the vm then
// refuses "notes.md" at run time. Every shape borrowed from the command side fails
// to COMPILE here rather than failing to evaluate, because the file scope's `path`
// is a plain string with no fields to reach into — measured, not assumed.
const postErroringGuard = `match: int(path) > 0
checks:
  - script: ./check.sh
`

// permitCheck permits whatever it is shown; refuseCheck refuses in its own words.
const permitCheck = `#!/bin/sh
cat >/dev/null
exit 0
`
const refuseCheck = `#!/bin/sh
cat >/dev/null
echo '{"reason":"this rule refuses"}'
exit 1
`

// T027_01: a gate trigger match that errors at the PRE hook point refuses.
//
// The pre half, restated here rather than assumed, because the asymmetry-turned-
// symmetry only means something if both sides are measured against the same running
// build. This is 014_01's claim; if it ever stops holding, the comparison below is
// comparing against nothing.
func TestT027_01_TheSameMatcherErrorRefusesBeforeTheAction(t *testing.T) {
	e, proj := project(t)
	e.Gate(proj, "npm-access", gateErroringMatcher, map[string]string{"check.sh": permitCheck})
	e.CommitAll(proj, "the project before the session")

	got := e.Run(proj, "s-027-01", "publish the package", Turns("done",
		Bash("b1", "npm publish"),
	))

	// The check permits, so nothing but the engine's answer to the matcher error can
	// refuse this command.
	if got.Permitted() {
		t.Fatalf("a matcher that could not be evaluated permitted the command at the pre-tool "+
			"point — 014's claim no longer holds, so nothing in this suite is comparing two "+
			"live behaviours:\n%s", got.Output)
	}
	if !got.Saw("npm-access") {
		t.Errorf("the refusal does not name the gate whose matcher failed:\n%s", got.Output)
	}
}

// T027_02: a file-guard whose match errors REFUSES at the cycle's hook point,
// exactly as the gate does at the pre-tool point.
//
// This test used to record the OPPOSITE, as a deliberate characterisation of an
// asymmetry: the after-check reported the error to a stream nobody reads and
// skipped the guard, so the binding was not consulted and the turn ended normally.
// Its own failure message said the asymmetry was very likely wrong and asked
// whoever closed it to update the test rather than revert the engine. That is what
// happened, and this is the update.
//
// The asymmetry was a fail-open. A match that cannot be evaluated has not answered
// "this file does not concern me" — it has not answered at all, and treating the
// two alike is the same mistake as a check exiting 0 because it could not run. The
// refusal holds the turn, names the guard, and quotes the expression.
//
// What still differs from the pre-tool point is the CHANNEL, not the verdict: a
// Post event is reported after the change is on disk, so the reason arrives as a
// blocking error at Stop rather than denying the action outright.
func TestT027_02_AMatcherErrorAtTheCyclesHookPointRefuses(t *testing.T) {
	e, proj := project(t)
	// A file-guard whose match compiles and cannot be evaluated on the settled file.
	// See postErroringGuard for why this shape and not one borrowed from the gate side.
	e.FileGuard(proj, "post-error", postErroringGuard, map[string]string{"check.sh": refuseCheck})
	e.CommitAll(proj, "the project before the session")

	e.Run(proj, "s-027-02", "write a note", Turns("done",
		Write("w1", "notes.md", "hello\n"),
	))

	told := strings.Join(e.BlockingErrors(proj, "s-027-02"), "\n")

	if !strings.Contains(told, "post-error") {
		t.Fatalf("a matcher that cannot be evaluated must hold the turn and name the guard, "+
			"not be read as a rule that did not apply:\n%s", told)
	}
	// The reason has to be actionable, or the author is told only that something went
	// wrong somewhere. The expression itself is what they have to fix.
	if !strings.Contains(told, "int(path)") {
		t.Errorf("the refusal does not quote the matcher that could not be evaluated:\n%s", told)
	}
}

// T027_03: the control — a Post file-guard whose match CAN be answered is consulted,
// and its refusal blocks the cycle.
//
// Without this, T027_02 passes against an engine where no Post file-guard fires at
// all, where the guard folder was misplaced, or where the check could not be
// executed. Same hook point, same kind of subject, same refusing check; the only
// difference is a match the engine can evaluate.
func TestT027_03_AnAnswerablePostMatcherIsConsulted(t *testing.T) {
	e, proj := project(t)
	e.FileGuard(proj, "post-sound", soundPostGuard, map[string]string{"check.sh": refuseCheck})
	e.CommitAll(proj, "the project before the session")

	got := e.Run(proj, "s-027-03", "write a note", Turns("done",
		Write("w1", "notes.md", "hello\n"),
	))

	if len(e.StopContinuations(proj, "s-027-03")) == 0 {
		t.Fatalf("a Post file-guard with an answerable match and a refusing check did not block "+
			"the cycle, so T027_02's refusal proves nothing:\n%s", got.Output)
	}
	told := strings.Join(e.BlockingErrors(proj, "s-027-03"), "\n")
	if !strings.Contains(told, "post-sound") {
		t.Errorf("the refusal did not name the guard that produced it:\n%s", told)
	}
}

// T027_04: a match that declines at the cycle's hook point permits.
//
// The third answer, and the one that separates "skipped because it errored" from
// "skipped because it said no". Both leave the cycle unblocked, so T027_02 alone
// cannot tell them apart — an engine that skipped EVERY Post file-guard would
// satisfy it. Here the match is answerable and answers no, which must permit.
func TestT027_04_APostMatcherThatDeclinesPermits(t *testing.T) {
	e, proj := project(t)
	e.FileGuard(proj, "post-sound", soundPostGuard, map[string]string{"check.sh": refuseCheck})
	e.CommitAll(proj, "the project before the session")

	// A file the match does not select: the guard narrows on ".md".
	e.Run(proj, "s-027-04", "write a text file", Turns("done",
		Write("w1", "notes.txt", "hello\n"),
	))

	told := strings.Join(e.BlockingErrors(proj, "s-027-04"), "\n")
	if strings.Contains(told, "post-sound") {
		t.Fatalf("a Post match that answered 'no' refused anyway:\n%s", told)
	}
}
