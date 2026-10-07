package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

const skillRule = "match: \"docs/**\"\nrequire:\n  - citation: {source_types: [user]}\nchecks:\n  - script: ./check.sh\n"

// T003_27: a refused `require:` row stays `fail` while its input is still in the
// range. Stop 1 refuses on the unmet citation requirement; Stop 2, with nothing
// changed, must refuse again and the row must still be `fail` in `sr-checks status`
// — it is never rewritten to a stale `skip` while the file is in base..head.
// sr:proves checks/requirements-before-checks
// sr:proves cache/finished-verdicts-reused
func TestT003_27_ARefusedRequireStaysFailed(t *testing.T) {
	e, proj, _ := project(t, skillRule)

	e.Run(proj, "s-003-27", "write the doc", Turns("done", harness.CommitFile("c1", "docs/a.md", "steps", "add a")))
	if out := e.CheckVerify(proj, "s-003-27", "origin/main", "HEAD"); out.Code == 0 || !strings.Contains(out.Output, "citation") {
		t.Fatalf("premise: Stop 1 should leave the requirement failing:\n%s", out.Output)
	}

	r := e.StopNow(proj, "s-003-27", false)
	if !harness.Blocked(r) || !strings.Contains(r.Output, "citation") {
		t.Fatalf("Stop 2 with nothing changed did not refuse on the requirement:\n%s", r.Output)
	}
	out := e.CheckVerify(proj, "s-003-27", "origin/main", "HEAD")
	if out.Code == 0 || !strings.Contains(out.Output, "citation") {
		t.Fatalf("the refused requirement vanished from verify at Stop 2:\n%s", out.Output)
	}
	for _, run := range e.CacheRecords(proj) {
		for _, c := range run.Checks {
			if strings.HasPrefix(c.Kind, "require:") && c.Status == "skip" {
				t.Fatalf("a requirement row was rewritten to skip though its input never left the range: %+v", c)
			}
		}
	}
}
