package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T041_63: `sr-checks run` from a sub-agent, over a rule that requires a citation. Where a
// sub-agent's session cannot be tied to the user's conversation (CapSubagentParentLink
// absent) the check cannot be judged there: sr-checks says so, naming the main agent, and
// the run is an error, not a verdict. Elsewhere it resolves citations as the main agent does.
func TestT041_63_ChecksFromASubagentThatCannotCite(t *testing.T) {
	const guard = `match: "memories/**"
require:
  - citation: {source_types: [tool_result]}
`
	e, proj := guardedUncited(t, guard)
	sub := subagentScript(t, harness.Turns("sub done",
		harness.CommitFile("sb1", "memories/a.md", "# a", "write a"),
		Bash("sb2", "sr-checks run --base HEAD~1 --head HEAD > sub-checks.out 2>&1; echo $? > sub-checks.rc"),
	))
	e.Run(proj, "s-041-63", prompt, Turns("done", harness.Dispatch("d1", "write it down", sub, "")))
	out := readProj(t, proj, "sub-checks.out")
	rc := strings.TrimSpace(readProj(t, proj, "sub-checks.rc"))
	const unlinked = "cannot be tied to the user's conversation"
	if harness.HasCap(t, harness.CapSubagentParentLink) {
		if strings.Contains(out, unlinked) {
			t.Errorf("a harness that links sub-agents refused to resolve citations from one:\n%s", out)
		}
		return
	}
	if rc == "0" || !strings.Contains(out, unlinked) || !strings.Contains(out, "main agent") {
		t.Fatalf("sr-checks from a sub-agent that cannot cite: exit %s, want an error naming the main agent:\n%s", rc, out)
	}
	// Not a verdict: nothing stored says the citation failed, so the main agent's run judges it.
	if strings.Contains(out, "FAIL") {
		t.Errorf("the run reported a FAIL verdict for a check it could not judge:\n%s", out)
	}
}
