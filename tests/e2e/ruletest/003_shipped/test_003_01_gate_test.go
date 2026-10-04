package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// T003_01: a rule change whose tests fail is refused by the shipped rule, with the doctor's
// report; fixing the rule lets it through, and the verdict is stored for CI's verify.
func TestT003_01_ARuleChangeWhoseTestsFailIsRefused(t *testing.T) {
	p := repoWithShippedRule(t)
	p.Write(".sloprail/gate/no-curl/gate.yaml", gateYAML)
	p.Write(".sloprail/gate/no-curl/no.sh", gateScript)
	caseFile(p, "refuses-curl", "refuse", "curl -s https://example.com")
	caseFile(p, "permits-ls", "permit", "ls -la")
	commit(p, "the no-curl gate, with its cases")
	p.Git("", "checkout", "-q", "-b", "change")

	// the change: the gate stops catching curl, and the case that expects the refusal now fails
	p.Write(".sloprail/gate/no-curl/gate.yaml", "on:\n  - event: PreCommandInvoke\n    match: any(event.invocations, .bin == \"wget\")\nchecks:\n  - script: ./no.sh\n")
	commit(p, "no-curl: match wget")

	res := run(p)
	require.Equal(t, 1, res.Code, res.Output)
	require.Contains(t, res.Output, "the rule gate/no-curl is not proved by its tests")
	require.Contains(t, res.Output, "refuses-curl")
	require.Contains(t, res.Output, "expected the engine to refuse, but it permitted")
	require.Contains(t, res.Output, `file-guard "rule-tests"`)

	// fix the rule: back to curl
	p.Write(".sloprail/gate/no-curl/gate.yaml", gateYAML)
	commit(p, "no-curl: match curl again")
	res = run(p)
	require.Equal(t, 0, res.Code, res.Output)

	// CI's verify only reads: it finds the stored pass
	res = p.Sr("", "sr-checks", "verify", "--base", "main", "--head", "HEAD")
	require.Equal(t, 0, res.Code, res.Output)
}

// T003_02: the rollout. A new rule must land with tests; one that stood with none is
// grandfathered until its first case, and cannot be changed through by deleting its cases.
func TestT003_02_TheRollout(t *testing.T) {
	// a new rule, no cases: refused
	p := repoWithShippedRule(t)
	commit(p, "the project")
	p.Git("", "checkout", "-q", "-b", "newrule")
	p.Write(".sloprail/gate/no-curl/gate.yaml", gateYAML)
	p.Write(".sloprail/gate/no-curl/no.sh", gateScript)
	commit(p, "a new rule, no cases")
	res := run(p)
	require.Equal(t, 1, res.Code, res.Output)
	require.Contains(t, res.Output, "gate/no-curl")
	require.Contains(t, res.Output, "no cases")

	// the same rule with a refuse and a permit case: permitted
	caseFile(p, "refuses-curl", "refuse", "curl -s https://example.com")
	caseFile(p, "permits-ls", "permit", "ls -la")
	commit(p, "its cases")
	res = run(p)
	require.Equal(t, 0, res.Code, res.Output)

	// a rule that stood with no cases is grandfathered: changing it is not blocked
	q := repoWithShippedRule(t)
	q.Write(".sloprail/gate/no-curl/gate.yaml", gateYAML)
	q.Write(".sloprail/gate/no-curl/no.sh", gateScript)
	commit(q, "a rule written before tests existed")
	q.Git("", "checkout", "-q", "-b", "tweak")
	q.Write(".sloprail/gate/no-curl/no.sh", gateScript+"# a clearer reason, soon\n")
	commit(q, "tweak the script")
	res = run(q)
	require.Equal(t, 0, res.Code, res.Output)

	// ... but once it has a case, the case must pass
	caseFile(q, "claims-wrongly", "permit", "curl x")
	commit(q, "a case that is wrong")
	res = run(q)
	require.Equal(t, 1, res.Code, res.Output)
	require.Contains(t, res.Output, "claims-wrongly")
}
