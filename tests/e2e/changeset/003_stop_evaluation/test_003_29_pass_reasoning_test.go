package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_29: a judge's PASS keeps its reasoning, as a fail does. Stored in the
// check's metadata (`checks.metadata.reasoning`) and shown by `sr-checks status`
// and `sr-checks sql`, so a range that was let through can be asked why.
func TestT003_29_APassKeepsItsReasoning(t *testing.T) {
	const why = "PASS-WHY-THE-DOC-HOLDS-UP"
	e, proj := judgeProject(t, `{"pass": true, "reasoning": "`+why+`"}`)

	e.Run(proj, "s-003-29", "write the docs", Turns("done", harness.CommitFile("c1", "docs/a.md", "the release is Friday", "add a")))
	if blocked := e.BlockingErrorsFrom(proj, "s-003-29", "Stop"); len(blocked) != 0 {
		t.Fatalf("premise: the passing judge refused:\n%s", strings.Join(blocked, "\n"))
	}

	if out := e.ChecksStatus(proj, "s-003-29"); !strings.Contains(out, "pass") || !strings.Contains(out, why) {
		t.Fatalf("sr-checks status does not show the pass's reasoning:\n%s", out)
	}
	sql := e.ChecksSQL(proj, "s-003-29", "select status, json_extract(metadata, '$.reasoning') as why from checks where kind like 'check[%:judge:%'")
	if !strings.Contains(sql.Output, `"pass"`) || !strings.Contains(sql.Output, why) {
		t.Fatalf("checks.metadata.reasoning is not stored for the pass:\n%s", sql.Output)
	}
}
