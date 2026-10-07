package e2e

import "testing"

// A SECOND, ordinary gate that did not launch anything.
//
// It refuses writes under secrets/, which is the kind of rule a project has
// regardless of whether anything judges anything. Nothing about it knows an
// agent was launched. It is a PreFileWrite gate so it acts at pre-tool, the same moment
// the launched agent's write is about to land — which is where it must still bite.
const refuseSecrets = `on:
  - event: PreFileWrite
    match: event.path startsWith "secrets/"
checks:
  - script: ./refuse.sh
`

const refuseSecretsScript = `#!/bin/sh
cat >/dev/null
echo '{"reason":"secrets/ is off limits"}'
exit 1
`

// judgeOnlyScript launches an agent and does nothing else. No counter: with the
// guard in place there is no recursion to bound, and T015_01 is where the
// runaway is measured.
const judgeOnlyScript = `#!/bin/sh
cat >/dev/null
echo ran >> ledger.txt
sr-agent --harness {{harness}} --model size-xs "judge this note" >/dev/null 2>&1
exit 0
`

// T015_02: a launched agent is still guarded by every rule that did not launch
// it.
//
// This is the half that rules out the scope answer — "the engine declines to
// enforce inside a session it started". That would pass T015_01 and fail here,
// which is exactly why the guard is keyed to the rule's NAME rather than to the
// session. A judging agent that legitimately edits files is still an agent
// editing this project's files.
//
// The launched agent tries to write under secrets/. The rule that launched it
// (judge-notes) is correctly not enforced; the unrelated rule (no-secrets) must
// still refuse.
// sr:proves checks/check-launched-agent-does-not-reenter-its-rule
func TestT015_02_LaunchedAgentIsStillGuardedByOtherRules(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "judge-notes", judgeByAgent, map[string]string{"judge.sh": e.ForHarness(judgeOnlyScript)})
	e.Gate(proj, "no-secrets", refuseSecrets, map[string]string{"refuse.sh": refuseSecretsScript})
	e.InstallClaudeShim(proj)
	// The launched agent reaches for a path the OTHER rule guards.
	e.InnerScenario(proj, Turns("judged", Write("i1", "secrets/leak.md", "oops")))

	got := e.Run(proj, "s-015-02", "write a note", Turns("done",
		Write("w1", "notes/first.md", "hello"),
	))
	t.Logf("judge ledger: %v", gateLedgerLines(t, proj, "judge-notes", "ledger.txt"))

	// The launching rule ran, so an agent really was launched — without this the
	// test could pass by the check never firing at all.
	if len(gateLedgerLines(t, proj, "judge-notes", "ledger.txt")) == 0 {
		t.Fatalf("the launching guardrail never ran, so nothing was launched:\n%s", got.Output)
	}

	// The refusal is observed in the guarded tree rather than in the outer
	// stream: the write was attempted by the INNER session, whose output the
	// shim discards, so what proves it was stopped is that the file does not
	// exist while the agent that asked for it has finished.
	if e.Wrote(proj, "secrets/leak.md") {
		t.Fatalf("a launched agent wrote under a path an unrelated guardrail forbids — "+
			"the guard disabled more than the rule that launched it:\n%s", got.Output)
	}
}

// T015_03: the launched agent's own legitimate write goes through.
//
// The complement of T015_02, and the reason this is not simply "refuse
// everything inside a launched agent". A judging agent exists to do work; a
// guard that stopped its writes would make the launching rule useless in a
// different way.
// sr:proves checks/check-launched-agent-does-not-reenter-its-rule
func TestT015_03_LaunchedAgentCanStillDoItsWork(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "judge-notes", judgeByAgent, map[string]string{"judge.sh": e.ForHarness(judgeOnlyScript)})
	e.Gate(proj, "no-secrets", refuseSecrets, map[string]string{"refuse.sh": refuseSecretsScript})
	e.InstallClaudeShim(proj)
	// A path judge-notes guards (notes/) and no-secrets does not.
	e.InnerScenario(proj, Turns("judged", Write("i1", "notes/judged.md", "ok")))

	got := e.Run(proj, "s-015-03", "write a note", Turns("done",
		Write("w1", "notes/first.md", "hello"),
	))

	if !e.Wrote(proj, "notes/judged.md") {
		t.Fatalf("the launched agent's own write was stopped; it is guarded by the very rule that launched it:\n%s", got.Output)
	}
}
