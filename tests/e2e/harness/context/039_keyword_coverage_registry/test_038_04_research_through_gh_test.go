package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// GitHub research happens against a declared scanner, through gh:
//
//   - github-research-through-gh (PreToolUse) refuses WebSearch outright and a
//     WebFetch of a GitHub content host, pointing at gh + a declared scanner;
//   - search-needs-declared-scanner (PreCommandInvoke) refuses a gh search —
//     however the command line wraps it — until scanner-declared has logged a
//     scanner this session.
//
// Found on real security-scan runs: with WebSearch available Haiku researched
// through it and never declared a scanner, so verify-scanner-coverage (Stop,
// scanner-declared active only) never engaged.

// researchProject installs the example with a stub `gh` under .stub/, so a gh
// call the gates PERMIT runs the stub rather than reaching GitHub. The stub is
// put on PATH by an `export …;` ahead of the command — a sequence, which is also
// one of the wrapped forms the gates must see through.
func researchProject(t *testing.T) (*harness.Env, string) {
	t.Helper()
	return researchProjectOn(t, New(t))
}

func researchProjectOn(t *testing.T, e *harness.Env) (*harness.Env, string) {
	t.Helper()
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.WriteExecutable(proj, ".stub/gh", "#!/bin/sh\necho stub-gh \"$@\"\n")
	e.WriteExecutable(proj, ".stub/curl", "#!/bin/sh\necho stub-curl \"$@\"\n")
	e.WriteExecutable(proj, ".stub/wget", "#!/bin/sh\necho stub-wget \"$@\"\n")
	e.WriteFile(proj, "sub/.keep", "")
	e.CommitAll(proj, "install")
	return e, proj
}

// stubbed runs a gh command line against the stub.
func stubbed(command string) string { return `export PATH="$PWD/.stub:$PATH"; ` + command }

// T038_18: a scanner declared in an EARLIER TURN still counts. The context closes
// at the first turn's passing Stop, so a rule reading context[...].active would
// refuse this; the registry persists for the session.
func TestT038_18_ScannerFromEarlierTurnStillCounts(t *testing.T) {
	e, proj := researchProject(t)
	const sess = "s-038-18"
	res := e.Run(proj, sess, "research guardrails", Turns("done",
		Write("w1", "scanners/mine/scanner.yaml", activeScanner),
		Bash("b1", stubbed(`gh search repos guardrail llm agent`)),
	).ThenCommit("write the files"))
	if res.Refused() {
		t.Fatalf("turn 1 refused:\n%s", res.Output)
	}
	if active, _ := e.ContextState(proj, sess, "scanner-declared"); active {
		t.Fatalf("precondition: the context should have closed at the passing Stop")
	}
	res = e.Run(proj, sess, "search a bit more", Turns("done",
		Bash("b2", stubbed(`gh search issues guardrail llm agent`)),
	).ThenCommit("write the files"))
	if res.Refused() {
		t.Fatalf("a search in a later turn, with the scanner declared earlier, was refused:\n%s", res.Output)
	}
}
