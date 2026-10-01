package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

const skillRule = "match: \"docs/**\"\nrequire:\n  - skill: document-topic\nchecks:\n  - script: ./check.sh\n"

// T003_27: a refused `require:` row stays `fail` while its input is still in the
// range. Stop 1 refuses on the unmet skill requirement; Stop 2, with nothing
// changed, must refuse again and the row must still be `fail` in `sr-checks status`
// — it is never rewritten to a stale `skip` while the file is in base..head.
func TestT003_27_ARefusedRequireStaysFailed(t *testing.T) {
	e, proj, _ := project(t, skillRule)

	e.Run(proj, "s-003-27", "write the doc", Turns("done", harness.CommitFile("c1", "docs/a.md", "steps", "add a")))
	if out := e.ChecksStatus(proj, "s-003-27", "--failing"); !strings.Contains(out, "require:skill:document-topic") || !strings.Contains(out, "fail") {
		t.Fatalf("premise: Stop 1 should leave the requirement failing:\n%s", out)
	}

	r := e.StopNow(proj, "s-003-27", false)
	if !harness.Blocked(r) || !strings.Contains(r.Output, "document-topic") {
		t.Fatalf("Stop 2 with nothing changed did not refuse on the requirement:\n%s", r.Output)
	}
	out := e.ChecksStatus(proj, "s-003-27", "--failing")
	if !strings.Contains(out, "require:skill:document-topic") || !strings.Contains(out, "fail") {
		t.Fatalf("the refused requirement vanished from status at Stop 2:\n%s", out)
	}
	sql := e.ChecksSQL(proj, "s-003-27", "select count(*) as n from checks where kind like 'require:%' and status = 'skip'")
	if !strings.Contains(sql.Output, `"n": 0`) && !strings.Contains(sql.Output, `"n":0`) {
		t.Fatalf("a requirement row was rewritten to skip though its input never left the range:\n%s", sql.Output)
	}
}
