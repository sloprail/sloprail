package e2e

import (
	"strings"
	"testing"
)

// This file drives the intake SKIP channel that the fixed example now delivers:
// an explicit #skip excuses a message (via the skip-declared context the gate
// reads with --owner), and a skip persists across cycles. The task-mapping admit
// is covered by T036_02.

// T036_04: an explicit #skip excuses the user message, so the gate ADMITS.
//
// The redesigned skip channel: the agent declares `#skip <line>` in its prose,
// naming the message line(s) that need no task. The skip-declared context enters
// on that tag (inside a hook, where `sr-session state set` is in scope), logs
// skip:<transcript>:<line>-<line> to its own registry, and the intake gate reads
// it via `state list --owner skip-declared` and subtracts the excused ref. The one
// user message (line 1) is excused, the residue empties, and the Stop is admitted.
func TestT036_04_ExplicitSkipAdmits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-036-04"
	// The one user message is at transcript line 1; #skip 1 excuses it.
	res := e.Run(proj, sess, "just saying hi, no task needed", Turns("done",
		Say("m1", "This is a greeting that needs no task. #skip 1"),
	))

	// The skip context logged the excused message under its own name — the exact
	// ref shape the gate collects, so this is a real skip and not an empty turn.
	reg := e.GuardrailState(proj, sess, "skip-declared", "skip:")
	if len(reg) == 0 {
		t.Fatalf("precondition: the skip-declared context logged no skip; registry=%v", reg)
	}
	if !anyKeyHasSuffix(reg, ":1-1") {
		t.Fatalf("the skip context did not log the line-1 message ref; registry keys=%v", keysOf(reg))
	}

	// With the message excused, the gate admits.
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Errorf("the gate refused a session whose only message was explicitly skipped:\n%v", blocks)
	}
	if res.Refused() {
		t.Errorf("unexpected refusal:\n%s", res.Output)
	}
}

// T036_05: a #skip that names a DIFFERENT line does NOT excuse the actual message
// — the gate still refuses, and only the named line is excused.
//
// The control that proves the skip is per-ref, not a blanket "any skip clears
// everything": the one user message is at line 1, but the agent skips line 9 (a
// line that is not the user message). The skip context logs skip:...:9-9, which
// does not match the residue's :1-1, so the message stays unaccounted and the gate
// refuses. Without this, a skip test could pass merely because SOME skip was
// present, regardless of whether it named the right message.
func TestT036_05_SkipOfWrongLineStillRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-036-05"
	res := e.Run(proj, sess, "please handle a real task", Turns("done",
		Say("m1", "Marking an unrelated line. #skip 9"),
	))

	// A skip WAS logged (for line 9), so the context ran — but it does not match
	// the line-1 message.
	reg := e.GuardrailState(proj, sess, "skip-declared", "skip:")
	if !anyKeyHasSuffix(reg, ":9-9") {
		t.Fatalf("precondition: the line-9 skip was not logged; registry keys=%v", keysOf(reg))
	}

	// The line-1 message is still unaccounted → the gate refuses.
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("the gate admitted despite the actual message being unaccounted (only a wrong-line "+
			"skip was declared):\n%s", res.Output)
	}
	if !strings.Contains(strings.Join(blocks, "\n"), residueReason) {
		t.Errorf("refused, but not with the residue reason:\n%v", blocks)
	}
}

func anyKeyHasSuffix(m map[string]string, suffix string) bool {
	for k := range m {
		if strings.HasSuffix(k, suffix) {
			return true
		}
	}
	return false
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
