package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_29: a judge's PASS keeps its reasoning, as a fail does. Stored in the
// check's metadata (`reasoning`, on the sloprail/checks branch), so a range that was
// let through can be asked why.
func TestT003_29_APassKeepsItsReasoning(t *testing.T) {
	const why = "PASS-WHY-THE-DOC-HOLDS-UP"
	e, proj := judgeProject(t, `{"pass": true, "reasoning": "`+why+`"}`)

	e.Run(proj, "s-003-29", "write the docs", Turns("done", harness.CommitFile("c1", "docs/a.md", "the release is Friday", "add a")))
	if blocked := e.BlockingErrorsFrom(proj, "s-003-29", "Stop"); len(blocked) != 0 {
		t.Fatalf("premise: the passing judge refused:\n%s", strings.Join(blocked, "\n"))
	}

	var stored bool
	for _, run := range e.CacheRecords(proj) {
		for _, c := range run.Checks {
			reasoning, _ := c.Metadata["reasoning"].(string)
			stored = stored || (strings.Contains(c.Kind, ":judge:") && c.Status == "pass" && strings.Contains(reasoning, why))
		}
	}
	if !stored {
		t.Fatalf("the pass's reasoning is not stored in the check's metadata: %+v", e.CacheRecords(proj))
	}
}
