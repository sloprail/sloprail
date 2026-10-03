package e2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// preventGate is the PREVENTION half of a grounded rule: a gate on PreFileWrite and
// PreFileDelete that refuses a change to memories/ carrying no citation of the
// user's words, before it lands. record.sh notes what it was handed and fails
// closed on a write whose result the engine could not compute.
const preventGate = `on:
  - event: PreFileWrite
    match: event.path startsWith "memories/"
  - event: PreFileDelete
    match: event.path startsWith "memories/"
require:
  - citation: {source_types: [user]}
checks:
  - script: ./record.sh
`

// settledGuard is the plain file-guard of the same rule: the same requirement,
// judged on the settled file at Stop.
const settledGuard = `match: "memories/**"
deletions: include
require:
  - citation: {source_types: [user]}
checks:
  - script: ./record.sh
`

// installPre installs the grounded rule as a gate plus a plain file-guard, the two
// halves sharing a name, and each recording what its check was handed.
func installPre(e *harness.Env, proj string) {
	e.Gate(proj, "grounded-memories", preventGate, map[string]string{"record.sh": failClosedRecordScript})
	e.FileGuard(proj, "grounded-memories", settledGuard, map[string]string{"record.sh": recordScript})
}

// preLedger is what both halves' checks were handed, the gate's (Pre events)
// first, then the file-guard's (Post events).
func preLedger(e *harness.Env, proj string) []string {
	return append(e.GateLedgerLines(proj, "grounded-memories", "ledger"),
		e.FileGuardLedgerLines(proj, "grounded-memories", "ledger")...)
}

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

// guardedPre is a project with the grounded rule installed as gate + file-guard.
func guardedPre(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPre(e, proj)
	e.CommitAll(proj, "baseline")
	return e, proj
}

func guarded(t *testing.T, guard string) (*harness.Env, string) {
	t.Helper()
	return guardedOn(t, New(t), guard)
}

// guardedUncited is guarded with the commit-time cite gate off (see NewUncited).
func guardedUncited(t *testing.T, guard string) (*harness.Env, string) {
	t.Helper()
	return guardedOn(t, NewUncited(t), guard)
}

func guardedOn(t *testing.T, e *harness.Env, guard string) (*harness.Env, string) {
	t.Helper()
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "grounded-memories", guard, map[string]string{"record.sh": recordScript})
	e.CommitAll(proj, "baseline")
	return e, proj
}

// T041_01: the harness Write tool carries no citation, so a guarded write is
// refused before it lands, and the refusal names the grounded way.
func TestT041_01_WriteToolIsRefused(t *testing.T) {
	e, proj := guardedPre(t)

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
	e, proj := guardedPre(t)

	res := e.Run(proj, "s-041-02", prompt, Turns("done",
		Bash("b1", `sr-file write memories/decisions.md --cite:user 'adopt a decision log' --content '# decisions'`),
	))
	if res.Refused() {
		t.Fatalf("a cited sr-file write was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, "memories/decisions.md") {
		t.Fatalf("the cited write did not land:\n%s", res.Output)
	}
	entries := ledger(t, preLedger(e, proj))
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
	e, proj := guardedPre(t)

	res := e.Run(proj, "s-041-03", prompt, Turns("done",
		Bash("b1", `sr-file write memories/decisions.md --cite:user 'the user never said this' --content '# decisions'`),
	))
	if !res.Refused() {
		t.Fatalf("a write citing words the user never said was permitted:\n%s", res.Output)
	}
	if e.Exists(proj, "memories/decisions.md") {
		t.Errorf("the write landed")
	}
	if !res.Saw("does not resolve") {
		t.Errorf("the refusal does not carry sr-file's own reason:\n%s", res.Output)
	}
}

// T041_13: a pure sr-file line whose dry run fails for a reason other than its
// citations (here an --old-string that is not in the file) is refused, and the
// refusal quotes sr-file's own error rather than a generic "could not compute".
func TestT041_13_DryRunFailureIsQuoted(t *testing.T) {
	e, proj := guardedPre(t)
	e.WriteFile(proj, "memories/decisions.md", "# decisions\n")
	e.CommitAll(proj, "baseline")

	res := e.Run(proj, "s-041-13", prompt, Turns("done",
		Bash("b1", `sr-file edit memories/decisions.md --cite:user 'adopt a decision log' --old-string 'no such line' --new-string x`),
	))
	if !res.Refused() {
		t.Fatalf("an edit sr-file cannot compute was permitted:\n%s", res.Output)
	}
	if !res.Saw("--old-string not found") {
		t.Errorf("the refusal does not quote sr-file's reason:\n%s", res.Output)
	}
}

// T041_04: sr-file mixed with another program is never run ahead of time; its
// result is unknown, so the gate refuses it and nothing lands.
func TestT041_04_ImpureLineIsRefused(t *testing.T) {
	e, proj := guardedPre(t)

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
	installPre(e, proj)
	e.CommitAll(proj, "baseline")

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
	installPre(e, proj)
	e.CommitAll(proj, "baseline")

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

// T041_07: a plain file-guard judges the range at Stop: a commit that carries no
// citation is refused there, and one whose `Sloprail-Cites-User` trailer resolves
// against the user's own words passes.
func TestT041_07_AfterCheckJudgesTheRangesCitations(t *testing.T) {
	const afterGuard = `match: "memories/**"
require:
  - citation: {source_types: [user]}
`
	e, proj := guardedUncited(t, afterGuard)
	e.Run(proj, "s-041-07", prompt, Turns("done", Write("w1", "memories/a.md", "# a")).ThenCommit("write a note"))
	if blocks := strings.Join(e.BlockingErrorsFrom(proj, "s-041-07", "Stop"), "\n"); !strings.Contains(blocks, noCitation) {
		t.Errorf("an uncited commit was not refused at Stop for want of a citation:\n%s", blocks)
	}

	e2, proj2 := guarded(t, afterGuard)
	e2.Run(proj2, "s-041-07b", prompt, Turns("done", Write("w1", "memories/a.md", "# a")).
		ThenCommit("write a note", harness.CitesUser("adopt a decision log")))
	if blocks := e2.BlockingErrorsFrom(proj2, "s-041-07b", "Stop"); len(blocks) != 0 {
		t.Errorf("a commit citing the user's words was refused at Stop: %v", blocks)
	}
}
