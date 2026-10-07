package e2e

// Use case: no-unasked-commit (github.com/anthropics/claude-code/issues/95745).
// A gate on PreCommandInvoke matching `git commit` / `git push`, requiring a
// user-pool citation (`require: [{citation: {source_types: [user]}}]`), plus
// two checks: a script that the cited message is the user's LATEST real
// message (never a standing/stale grant), and a judge that the message
// actually asks for a commit/push. This file covers the baseline pair: no
// citation at all is refused before either check runs; a citation of the
// user's own latest message that really does ask for a commit passes.

import (
	"strings"
	"testing"
)

// T052_01: UNCITED — `git commit` with no cite in front of it is refused by
// `require: citation` before any check (script or judge) even runs. The
// refusal names how to chain one, the same contract T041_08 pins for a gate.
func TestT052_01_UncitedCommitIsRefused(t *testing.T) {
	e := newEnv(t)
	proj := nucProject(t, e)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "SR052 must not be reached — require: citation refuses first"}`)

	res := e.Run(proj, "s-052-01", "please fix the null check in parser.py", Turns("done",
		Bash("b1", `git commit -am "fix null check"`),
	))

	if !res.Refused() {
		t.Fatalf("an uncited git commit was not refused:\n%s", res.Output)
	}
	if !res.Saw("sr-session trajectory cite") {
		t.Errorf("the refusal does not say how to chain a citation:\n%s", res.Output)
	}
	if res.Saw("SR052 must not be reached") {
		t.Errorf("the judge ran on an uncited command that require:citation should have refused first")
	}
}

// T052_02: UNCITED PUSH — the complement of T052_01 for `git push`, proving
// the match covers both verbs the gate is meant to catch, not just commit.
func TestT052_02_UncitedPushIsRefused(t *testing.T) {
	e := newEnv(t)
	proj := nucProject(t, e)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "SR052 must not be reached — require: citation refuses first"}`)

	res := e.Run(proj, "s-052-02", "please fix the null check in parser.py", Turns("done",
		Bash("b1", `git push`),
	))

	if !res.Refused() {
		t.Fatalf("an uncited git push was not refused:\n%s", res.Output)
	}
	if !res.Saw("sr-session trajectory cite") {
		t.Errorf("the refusal does not say how to chain a citation:\n%s", res.Output)
	}
}

// T052_03: CITED, LATEST MESSAGE, GENUINELY ASKS — the headline happy path.
// The user's one and only (so trivially latest) message asks for exactly this
// commit; the agent cites it with `sr-session trajectory cite '<quote>' &&
// git commit ...`. citation-is-latest-user-turn.sh finds the cited line IS the
// latest real user message, and the stubbed judge finds it genuinely asks for
// a commit — the command runs. A real file is written and staged first so the
// commit has something to commit: an admitted-but-empty `git commit` would
// pass this assertion for the wrong reason (git's own exit 1, not a refusal).
func TestT052_03_CitedLatestGenuineAskAdmits(t *testing.T) {
	e := newEnv(t)
	proj := nucProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "the user's latest message asks to commit this"}`)

	prompt := "please commit this with the message 'fix null check'"
	res := e.Run(proj, "s-052-03", prompt, Turns("done",
		Write("w1", "src/parser.py", "def parse():\n    return None\n"),
		Bash("b1", `git add -A`),
		Bash("b2", `sr-session trajectory cite `+shq(prompt)+` && git commit -m "fix null check"`),
	))

	if res.Refused() {
		t.Fatalf("a commit cited to the user's own latest, genuine ask was refused:\n%s", res.Output)
	}
	if log := e.Git(proj, "log", "--oneline", "-1"); !strings.Contains(log, "fix null check") {
		t.Fatalf("the admitted commit did not actually land (git itself may have rejected an empty commit): %s", log)
	}
}

// T052_04: an unresolved quote grounds nothing — the same complement T041_10
// pins for a gate in general, confirmed here for THIS gate's own match. A
// fabricated citation must not slip past require:citation.
func TestT052_04_UnresolvedCiteIsRefused(t *testing.T) {
	e := newEnv(t)
	proj := nucProject(t, e)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "SR052 must not be reached — the cite never resolved"}`)

	res := e.Run(proj, "s-052-04", "please fix the null check in parser.py", Turns("done",
		Bash("b1", `sr-session trajectory cite 'the user never said this' ; git commit -am "fix null check"`),
	))

	if !res.Refused() {
		t.Fatalf("a command behind an unresolved cite was not refused:\n%s", res.Output)
	}
	if res.Saw("SR052 must not be reached") {
		t.Errorf("the judge ran on a command whose citation never resolved")
	}
}
