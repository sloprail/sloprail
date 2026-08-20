// Package e2e asks the matcher half of "a mechanism that fails must not read as
// approval" at the hook point that ends a cycle.
//
// 014 establishes it at the pre-tool point, at length: a matcher that cannot be
// EVALUATED is the engine unable to answer whether a rule applies, which is not
// the same as the rule being satisfied, so the action is refused. The engine's
// own comment there spells out the reasoning and calls skipping the binding the
// defect it was corrected for.
//
// The Post side is a separate implementation of the same step — `admits` in
// dispatch_post.go — and it does the opposite: a matcher that errors is reported
// on stderr and the binding is skipped. At this hook point stderr reaches
// nobody, because a Stop hook that permits exits 0 and no channel delivers at
// exit 0. So a rule bound after the fact whose matcher errors is not consulted,
// nothing refuses, and nothing says so.
//
// # Whether that is a defect or a decision
//
// It is a decision the code makes explicitly — `admits` documents "never matches
// everything, never matches nothing" for the COMPILE failure — and the compile
// case really is settled elsewhere: a matcher that will not compile makes the
// whole declaration Invalid at load, which the broken-declaration path now
// reports at this hook point too (see 026_06). The compile branch in `admits` is
// unreachable for a declaration that got this far.
//
// The EVALUATION failure is not settled elsewhere, and it is the one this suite
// is about. It is reachable with a declaration that loads cleanly, and it is the
// exact failure 014 refuses at the other hook point.
//
// # What this suite claims, and what it does not
//
// It does NOT claim the Post side must refuse. Refusing a cycle and refusing a
// pending action are different costs, and which is right here is the owner's
// call rather than a test's — an after-the-fact refusal blocks the turn, so a
// rule whose matcher errors on every cycle would wedge the session, and 026_08's
// reasoning (the author has to be able to fix the declaration from inside a
// cycle) applies with some force.
//
// What it claims is that the two hook points must not DISAGREE SILENTLY about
// the same expression. T027_01 pins the asymmetry as it stands, so that it is
// visible and deliberate rather than an accident of two implementations; if the
// engine is later made to refuse here, this test fails and says exactly what
// changed.
package e2e

import (
	"strings"
	"testing"
)

// erroringMatcher is a rule whose matcher cannot be evaluated against the event
// it is bound to.
//
// `.flags.access` is nil on a command that did not pass --access, and `len` of
// nil errors. The element shape commandmod declares covers `bin`, `argv` and
// `flags`, but `flags` is a map with no enumerated keys, so it types as Any and
// the accessor inside it is not checked at load. That is what makes this a rule
// that LOADS cleanly and fails at evaluation — the only way to reach the
// evaluation branch at all.
//
// Bound to PreCommandInvoke, which is the kind that carries `invocations`.
const erroringMatcher = `---
hooks:
  PreCommandInvoke:
    - matcher: any(invocations, len(.flags.access) > 0)
      hooks:
        - type: command
          command: ./h.sh
---

# A rule about npm publish --access, phrased the ordinary way
`

// soundPostRule is bound to a Post kind and its matcher can always be answered.
// The control that shows a Post binding fires at all in this project.
const soundPostRule = `---
hooks:
  PostFileCreate:
    - matcher: path endsWith ".md"
      hooks:
        - type: command
          command: ./h.sh
---

# Objects to any markdown file this cycle created
`

// postErroringMatcher is a rule bound to a Post kind whose matcher compiles and
// then cannot be evaluated.
//
// Finding one took some doing, and the reason is worth recording because it
// bounds how much of this can be tested at all. The evaluation branch is only
// reachable through a value the type checker could not describe, and the Post
// kinds are the best-typed events in the build: PostFileCreate, PostFileUpdate
// and PostFileDelete each carry `path` and nothing else, declared TypeString,
// and Stop carries no fields whatever. So every shape borrowed from 014 fails
// to COMPILE here rather than failing to evaluate — measured, not assumed:
//
//	any(path, # > 0)        builtin any takes only array (got string)
//	len(path.access) > 0    type string has no field access
//	path[0] == "x"          type string[int] is undefined
//
// A compile failure is a different path: it makes the whole declaration Invalid
// at load, which 026_06 now has the engine reporting at this hook point, so it
// would not exercise `admits` at all.
//
// `int(path)` is the one shape that gets through. The checker accepts the
// conversion because `int` of a string is well-formed in the expression
// language, and the vm then refuses "notes.md" at run time. An odd rule to
// write, and that is the honest position: on the Post kinds as they are declared
// today, the evaluation branch is nearly unreachable, and this suite is pinning
// a narrow edge rather than an everyday mistake.
const postErroringMatcher = `---
hooks:
  PostFileCreate:
    - matcher: int(path) > 0
      hooks:
        - type: command
          command: ./h.sh
---

# A rule whose matcher compiles and cannot be evaluated against the event
`

const permits = "#!/bin/sh\ncat >/dev/null\nexit 0\n"

// T027_01: a matcher that errors at the PRE hook point refuses, and the same
// expression is the one the engine treats differently after the fact.
//
// The pre half, restated here rather than assumed, because the asymmetry only
// means something if both sides are measured against the same running build.
// This is 014_01's claim; if it ever stops holding, the comparison below is
// comparing against nothing.
func TestT027_01_TheSameMatcherErrorRefusesBeforeTheAction(t *testing.T) {
	e, proj := project(t)
	e.Guardrail(proj, "npm-access", erroringMatcher, map[string]string{"h.sh": permits})
	commitGuardrails(e, proj)

	got := e.Run(proj, "s-027-01", "publish the package", Turns("done",
		Bash("b1", "npm publish"),
	))

	// The hook permits, so nothing but the engine's answer to the matcher error
	// can refuse this command.
	if got.Permitted() {
		t.Fatalf("a matcher that could not be evaluated permitted the command at the pre-tool "+
			"point — 014's claim no longer holds, so nothing in this suite is comparing two "+
			"live behaviours:\n%s", got.Output)
	}
	if !got.Saw("npm-access") {
		t.Errorf("the refusal does not name the guardrail whose matcher failed:\n%s", got.Output)
	}
}

// T027_02: a guardrail whose matcher errors REFUSES at the cycle's hook point,
// exactly as it does at the pre-tool point.
//
// This test used to record the opposite, as a deliberate characterisation of an
// asymmetry: `admits` reported the error to a stream nobody reads and returned
// false, so the binding was not consulted and the turn ended normally. Its own
// failure message said the asymmetry was very likely wrong and asked whoever
// closed it to update the test rather than revert the engine. That is what
// happened, and this is the update.
//
// The asymmetry was a fail-open. A matcher that cannot be evaluated has not
// answered "this rule does not apply" — it has not answered at all, and
// treating the two alike is the same mistake as a hook exiting 0 because it
// could not run. The refusal names the guardrail, quotes the expression, and
// says what to do: fix the matcher, or `enabled: false` if it is not ready.
//
// What still differs between the two hook points is the CHANNEL, not the
// verdict. The pre-tool point denies the action outright; here the cycle is
// held and the reason arrives as a blocking error, because a Post event is
// reported after the change is already on disk.
func TestT027_02_AMatcherErrorAtTheCyclesHookPointRefuses(t *testing.T) {
	e, proj := project(t)
	// Bound to a POST kind, with an expression that compiles and cannot be
	// evaluated. See postErroringMatcher for why this one and not a shape
	// borrowed from 014.
	e.Guardrail(proj, "post-error", postErroringMatcher,
		map[string]string{"h.sh": "#!/bin/sh\ncat >/dev/null\necho 'this rule refuses' >&2\nexit 1\n"})
	commitGuardrails(e, proj)

	e.Run(proj, "s-027-02", "write a note", Turns("done",
		Write("w1", "notes.md", "hello\n"),
	))

	told := strings.Join(e.BlockingErrors(proj, "s-027-02"), "\n")

	if !strings.Contains(told, "post-error") {
		t.Fatalf("a matcher that cannot be evaluated must hold the turn and name the guardrail, "+
			"not be read as a rule that did not apply:\n%s", told)
	}
	// The reason has to be actionable, or the author is told only that something
	// went wrong somewhere. The expression itself is what they have to fix.
	if !strings.Contains(told, "int(path)") {
		t.Errorf("the refusal does not quote the matcher that could not be evaluated:\n%s", told)
	}
}

// T027_03: the control — a Post rule whose matcher CAN be answered is consulted,
// and its refusal blocks the cycle.
//
// Without this, T027_02 passes against an engine where no Post binding fires at
// all, where the guardrail folder was misplaced, or where the hook could not be
// executed. Same hook point, same kind, same refusing script; the only
// difference is a matcher the engine can evaluate.
func TestT027_03_AnAnswerablePostMatcherIsConsulted(t *testing.T) {
	e, proj := project(t)
	e.Guardrail(proj, "post-sound", soundPostRule, map[string]string{
		"h.sh": "#!/bin/sh\ncat >/dev/null\necho 'this rule refuses' >&2\nexit 1\n",
	})
	commitGuardrails(e, proj)

	got := e.Run(proj, "s-027-03", "write a note", Turns("done",
		Write("w1", "notes.md", "hello\n"),
	))

	if strings.Count(got.Output, `"subtype":"success"`) < 2 {
		t.Fatalf("a Post rule with an answerable matcher and a refusing hook did not block the "+
			"cycle, so T027_02's skip proves nothing:\n%s", got.Output)
	}
	told := strings.Join(e.BlockingErrors(proj, "s-027-03"), "\n")
	if !strings.Contains(told, "post-sound") {
		t.Errorf("the refusal did not name the guardrail that produced it:\n%s", told)
	}
}

// T027_04: a matcher that declines at the cycle's hook point permits.
//
// The third answer, and the one that separates "skipped because it errored" from
// "skipped because it said no". Both leave the cycle unblocked, so T027_02 alone
// cannot tell them apart — an engine that skipped EVERY Post binding would
// satisfy it. Here the matcher is answerable and answers no, which must permit.
func TestT027_04_APostMatcherThatDeclinesPermits(t *testing.T) {
	e, proj := project(t)
	e.Guardrail(proj, "post-sound", soundPostRule, map[string]string{
		"h.sh": "#!/bin/sh\ncat >/dev/null\necho 'this rule refuses' >&2\nexit 1\n",
	})
	commitGuardrails(e, proj)

	// A file the matcher does not admit: the rule narrows on ".md".
	e.Run(proj, "s-027-04", "write a text file", Turns("done",
		Write("w1", "notes.txt", "hello\n"),
	))

	told := strings.Join(e.BlockingErrors(proj, "s-027-04"), "\n")
	if strings.Contains(told, "post-sound") {
		t.Fatalf("a Post matcher that answered 'no' refused anyway:\n%s", told)
	}
}
