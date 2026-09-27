package e2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

const preventiveGuard = `match: "memories/**"
preventive: true
deletions: include
require:
  - citation: {source_types: [user]}
checks:
  - script: ./record.sh
`

type ledgerEntry struct {
	Kind        string `json:"kind"`
	Path        string `json:"path"`
	ResultKnown *bool  `json:"resultKnown"`
	N           int    `json:"n"`
	Quote       string `json:"quote"`
	Line        int    `json:"line"`
}

func ledger(t *testing.T, lines []string) []ledgerEntry {
	t.Helper()
	var out []ledgerEntry
	for _, l := range lines {
		var e ledgerEntry
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatalf("ledger line %q: %v", l, err)
		}
		out = append(out, e)
	}
	return out
}

func guarded(t *testing.T, guard string) (*harness.Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "grounded-memories", guard, map[string]string{"record.sh": recordScript})
	commitAll(t, proj)
	return e, proj
}

// T041_01: the harness Write tool carries no citation, so a guarded write is
// refused before it lands, and the refusal names the grounded way.
func TestT041_01_WriteToolIsRefused(t *testing.T) {
	e, proj := guarded(t, preventiveGuard)

	res := e.Run(proj, "s-041-01", prompt, Turns("done",
		Write("w1", "memories/decisions.md", "# decisions"),
	))
	if !res.Refused() {
		t.Fatalf("an uncited write to a guarded file was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, "memories/decisions.md") {
		t.Errorf("the uncited write landed")
	}
	if !res.Saw("sr-file") || !res.Saw("--cite:user") {
		t.Errorf("the refusal does not say how to ground the change:\n%s", res.Output)
	}
}

// T041_02: sr-file write with a resolving citation lands; the check is handed an
// exact result (resolved by running sr-file in resolve mode) and the citation.
func TestT041_02_CitedSRFileWriteLands(t *testing.T) {
	e, proj := guarded(t, preventiveGuard)

	res := e.Run(proj, "s-041-02", prompt, Turns("done",
		Bash("b1", `sr-file write memories/decisions.md --cite:user 'adopt a decision log' --content '# decisions'`),
	))
	if res.Refused() {
		t.Fatalf("a cited sr-file write was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, "memories/decisions.md") {
		t.Fatalf("the cited write did not land:\n%s", res.Output)
	}
	entries := ledger(t, e.FileGuardLedgerLines(proj, "grounded-memories", "ledger"))
	var pre *ledgerEntry
	for i := range entries {
		if entries[i].Kind == "PreFileCreate" {
			pre = &entries[i]
		}
	}
	if pre == nil {
		t.Fatalf("the check never saw a PreFileCreate: %+v", entries)
	}
	if pre.ResultKnown == nil || !*pre.ResultKnown {
		t.Errorf("sr-file's result was not resolved exactly: %+v", *pre)
	}
	if pre.N != 1 || pre.Quote != "adopt a decision log" || pre.Line == 0 {
		t.Errorf("the check was not handed the citation: %+v", *pre)
	}
	for _, en := range entries {
		if en.Kind == "PostFileCreate" && en.N != 1 {
			t.Errorf("the Post event at Stop lost the recorded citation: %+v", en)
		}
	}
}

// T041_03: a quote the user never said resolves to nothing, so the write is
// refused and does not land.
func TestT041_03_UnresolvedQuoteIsRefused(t *testing.T) {
	e, proj := guarded(t, preventiveGuard)

	res := e.Run(proj, "s-041-03", prompt, Turns("done",
		Bash("b1", `sr-file write memories/decisions.md --cite:user 'the user never said this' --content '# decisions'`),
	))
	if !res.Refused() {
		t.Fatalf("a write citing words the user never said was permitted:\n%s", res.Output)
	}
	if e.Exists(proj, "memories/decisions.md") {
		t.Errorf("the write landed")
	}
}

// T041_04: sr-file mixed with another program is never run ahead of time; its
// result is unknown, so a preventive rule refuses it and nothing lands.
func TestT041_04_ImpureLineIsRefused(t *testing.T) {
	e, proj := guarded(t, preventiveGuard)

	res := e.Run(proj, "s-041-04", prompt, Turns("done",
		Bash("b1", `sr-file write memories/decisions.md --cite:user 'adopt a decision log' --content x && touch other.txt`),
	))
	if !res.Refused() {
		t.Fatalf("a line mixing sr-file with another program was permitted:\n%s", res.Output)
	}
	if e.Exists(proj, "memories/decisions.md") || e.Exists(proj, "other.txt") {
		t.Errorf("the refused line ran")
	}
}

// T041_05: an edit chained with harmless output glue is still resolved and
// permitted; the file holds the edited bytes.
func TestT041_05_EditWithEchoIsResolved(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "memories/decisions.md", "# decisions\n- none yet\n")
	e.FileGuard(proj, "grounded-memories", preventiveGuard, map[string]string{"record.sh": recordScript})
	commitAll(t, proj)

	res := e.Run(proj, "s-041-05", prompt, Turns("done",
		Bash("b1", `sr-file edit memories/decisions.md --old-string '- none yet' --new-string '- adopt a decision log' --cite:user 'adopt a decision log' && echo ok`),
	))
	if res.Refused() {
		t.Fatalf("a cited edit followed by echo was refused:\n%s", res.Output)
	}
	body := readProj(t, proj, "memories/decisions.md")
	if !strings.Contains(body, "- adopt a decision log") {
		t.Errorf("the edit did not apply: %q", body)
	}
}

// T041_06: with deletions included, `rm` is refused and a cited sr-file delete
// is permitted.
func TestT041_06_DeletionsNeedACitation(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "memories/old.md", "stale\n")
	e.FileGuard(proj, "grounded-memories", preventiveGuard, map[string]string{"record.sh": recordScript})
	commitAll(t, proj)

	res := e.Run(proj, "s-041-06", prompt, Turns("done", Bash("b1", `rm memories/old.md`)))
	if !res.Refused() || !e.Exists(proj, "memories/old.md") {
		t.Fatalf("an uncited rm of a guarded file was not refused:\n%s", res.Output)
	}

	res = e.Run(proj, "s-041-06b", prompt, Turns("done",
		Bash("b1", `sr-file delete memories/old.md --cite:user 'record the decision'`),
	))
	if res.Refused() {
		t.Fatalf("a cited sr-file delete was refused:\n%s", res.Output)
	}
	if e.Exists(proj, "memories/old.md") {
		t.Errorf("the cited delete did not remove the file")
	}
}

// T041_07: a non-preventive guard judges at Stop: an uncited change is refused
// there, and a cited one — whose citation was recorded at pre-tool — passes.
func TestT041_07_AfterCheckUsesRecordedCitations(t *testing.T) {
	const afterGuard = `match: "memories/**"
require:
  - citation: {source_types: [user]}
`
	e, proj := guarded(t, afterGuard)
	e.Run(proj, "s-041-07", prompt, Turns("done", Write("w1", "memories/a.md", "# a")))
	if len(e.BlockingErrorsFrom(proj, "s-041-07", "Stop")) == 0 {
		t.Errorf("an uncited change was not refused at Stop")
	}

	e2, proj2 := guarded(t, afterGuard)
	e2.Run(proj2, "s-041-07b", prompt, Turns("done",
		Bash("b1", `sr-file write memories/a.md --cite:user 'adopt a decision log' --content '# a'`),
	))
	if blocks := e2.BlockingErrorsFrom(proj2, "s-041-07b", "Stop"); len(blocks) != 0 {
		t.Errorf("a cited change was refused at Stop: %v", blocks)
	}
}
