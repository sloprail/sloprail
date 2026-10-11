package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A rule whose `subjects:` script puts its subjects in one batch, through the compiled
// binaries: sr-checks hands the batch to the real sr-agent, whose agent is a stand-in; the
// judge answers one verdict per subject, is asked again when it misses one, and each
// subject's verdict is stored, shown and replayed on its own.

const (
	batchRule = `match: "docs/**"
subjects: ./subjects.sh
checks:
  - judge: ./rubric.md.j2
`
	// One subject per doc, all in the batch "docs".
	batchSubjects = `#!/bin/sh
jq -c '[.changeset.files[] | {id: .path, files: [.path], batch: "docs"}]'
`
	batchRubric = `Judge each doc of the batch {{ batch.id }} on its own: {% for s in batch.subjects %}{{ s.id }}{% if not loop.last %}, {% endif %}{% endfor %}
{{ change }}
`
	batchMissingC = `{"verdicts": {"docs/a.md": {"pass": true, "reasoning": "dated"}, "docs/b.md": {"pass": true, "reasoning": "dated"}}}`
	batchFailB    = `{"verdicts": {"docs/a.md": {"pass": true, "reasoning": "dated"}, "docs/b.md": {"pass": false, "reasoning": "docs/b.md carries no date"}, "docs/c.md": {"pass": true, "reasoning": "dated"}}}`
	batchPassC    = `{"verdicts": {"docs/c.md": {"pass": true, "reasoning": "dated"}}}`
)

// BatchRow is one `sr-checks show --json` row.
type BatchRow struct {
	Subject string `json:"subject"`
	Batch   string `json:"batch"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Source  string `json:"source"`
}

func batchRows(t *testing.T, out string) map[string]BatchRow {
	t.Helper()
	start := strings.Index(out, "[")
	if start < 0 {
		t.Fatalf("no JSON listing in:\n%s", out)
	}
	var rows []BatchRow
	if err := json.NewDecoder(strings.NewReader(out[start:])).Decode(&rows); err != nil {
		t.Fatalf("unreadable JSON listing: %v\n%s", err, out)
	}
	judged := map[string]BatchRow{}
	for _, r := range rows {
		if strings.Contains(r.Kind, ":judge:") {
			judged[r.Subject] = r
		}
	}
	return judged
}

// T001_45: three docs in one batch are judged in one call. The first answer misses a doc and
// is sent back naming it; the second fails one doc, which alone is refused. show lists one
// row per doc with its batch, changeset prints the batch's payload, and after one doc
// changes only it is judged again, still handed as a batch.
// sr:proves fileguard/one-verdict-per-subject
// sr:proves fileguard/batch-ids-are-exact
// sr:proves fileguard/settled-subject-leaves-its-batch
// sr:proves fileguard/batch-payload-only-when-batched
func TestT001_45_ABatchedJudgeGivesEachSubjectItsOwnVerdict(t *testing.T) {
	e, proj := session(t)
	e.FileGuard(proj, "docs", batchRule, map[string]string{"subjects.sh": batchSubjects, "rubric.md.j2": batchRubric})
	base := e.CommitAll(proj, "the rule")
	for _, d := range []string{"a", "b", "c"} {
		e.WriteFile(proj, "docs/"+d+".md", "doc "+d+"\n")
	}
	e.CommitAll(proj, "add the docs")
	agent := answersInTurn(t, e, batchMissingC, batchFailB)

	shown := checks(e, proj, "changeset", "--rule", "docs", "--base", base, "--head", "HEAD")
	var cs struct {
		Subjects []any `json:"subjects"`
		Batches  []struct {
			ID       string         `json:"id"`
			Subjects []string       `json:"subjects"`
			Payload  map[string]any `json:"payload"`
		} `json:"batches"`
	}
	if err := json.Unmarshal([]byte(shown.Output[strings.Index(shown.Output, "{"):]), &cs); err != nil {
		t.Fatalf("changeset printed no JSON: %v\n%s", err, shown.Output)
	}
	if len(cs.Subjects) != 0 || len(cs.Batches) != 1 || cs.Batches[0].ID != "docs" || strings.Join(cs.Batches[0].Subjects, ",") != "docs/a.md,docs/b.md,docs/c.md" {
		t.Fatalf("changeset does not list the one batch:\n%s", shown.Output)
	}
	if _, ok := cs.Batches[0].Payload["subject"]; ok || cs.Batches[0].Payload["batch"] == nil {
		t.Fatalf("the batch's payload must carry batch, not subject:\n%s", shown.Output)
	}

	run := checks(e, proj, "run", "--base", base, "--head", "HEAD")
	if run.Code != 1 {
		t.Fatalf("run exited %d, want 1 (docs/b.md fails):\n%s", run.Code, run.Output)
	}
	contains(t, run.Output, "docs/b.md carries no date")
	if n := agentCalls(t, agent); n != 2 {
		t.Fatalf("the judge was asked %d times, want 2 (once for the batch, once more for the doc it missed)", n)
	}
	first, err := os.ReadFile(filepath.Join(agent, "prompt.1"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, string(first), "Judge each doc of the batch docs on its own: docs/a.md, docs/b.md, docs/c.md", `"docs/c.md"`, `{"verdicts": {"<id>"`)
	second, err := os.ReadFile(filepath.Join(agent, "prompt.2"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, string(second), `no entry for: "docs/c.md"`)

	rows := batchRows(t, checks(e, proj, "show", "--json", "--base", base, "--head", "HEAD").Output)
	for doc, want := range map[string]string{"docs/a.md": "pass", "docs/b.md": "fail", "docs/c.md": "pass"} {
		if r := rows[doc]; r.Status != want || r.Batch != "docs" {
			t.Fatalf("show: %s is %+v, want %s in batch docs (rows: %+v)", doc, r, want, rows)
		}
	}

	// docs/c.md changes: a and b keep their stored verdicts, c is judged again alone, as a batch.
	e.WriteFile(proj, "docs/c.md", "doc c, revised\n")
	e.CommitAll(proj, "revise c")
	if err := os.WriteFile(filepath.Join(agent, "answer.3"), []byte(batchPassC+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	again := checks(e, proj, "run", "--base", base, "--head", "HEAD")
	if again.Code != 1 || !strings.Contains(again.Output, "docs/b.md carries no date") {
		t.Fatalf("b's stored fail was not replayed: exit %d\n%s", again.Code, again.Output)
	}
	if n := agentCalls(t, agent); n != 3 {
		t.Fatalf("the judge was asked %d times in all, want 3 (only docs/c.md again)", n)
	}
	third, err := os.ReadFile(filepath.Join(agent, "prompt.3"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, string(third), "Judge each doc of the batch docs on its own: docs/c.md\n")
	rows = batchRows(t, checks(e, proj, "show", "--json", "--base", base, "--head", "HEAD").Output)
	if rows["docs/c.md"].Status != "pass" || rows["docs/a.md"].Status != "pass" {
		t.Fatalf("show after the revision: %+v", rows)
	}
}
