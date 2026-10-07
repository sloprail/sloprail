package e2e

// The citation requirement of pinned-spec-holds is evaluated PER FILE: changes-pinned-lines.sh
// decides for the one file it is asked about. A marker-carrying source file edited with its
// marker kept changes nothing pinned, so it needs no citation just because the spec beside it
// changed in the same range — only the spec's own change does.

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

const changeRuleAsk = "change rule 2 of the spec so goodwill refunds may exceed the charge"

// relaxSpecByScript is the agent rewriting rule 2 with a script, which no gate sees as a
// write: the change reaches the range as a commit only.
const relaxSpecByScript = `python3 -c "import pathlib; p=pathlib.Path('SPEC.md'); p.write_text(p.read_text().replace('charge amount.', 'charge amount, except goodwill refunds.'))"`

// editedCharge is src/charge.go with its marker kept and its body changed.
func editedCharge(t *testing.T, proj string) string {
	t.Helper()
	return strings.Replace(readFile(t, proj, "src/charge.go"), "amount <= charged", "charged >= amount", 1)
}

// T046_31: an uncited SPEC.md change is still refused, naming SPEC.md and not the source
// file edited beside it, and the refusal's own command (a follow-up commit that changes SPEC.md and
// carries the quote) grounds it.
func TestT046_31_AnUncitedSpecChangeIsRefusedNamingOnlyTheSpec(t *testing.T) {
	e := newEnvUncited(t)
	proj := pinnedSpecProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "the user asked to relax rule 2 for goodwill refunds"}`)

	sess := "s-046-31"
	e.Run(proj, sess, changeRuleAsk, Turns("done",
		Bash("b1", relaxSpecByScript),
	).ThenCommit("relax rule 2"))
	e.Run(proj, sess, "and tidy the comparison", Turns("done",
		harness.CommitFile("c2", "src/charge.go", editedCharge(t, proj), "reorder the comparison"),
	))
	refusals := e.AllBlockingErrorsFrom(proj, sess, "Stop")
	if len(refusals) == 0 {
		t.Fatalf("an uncited change to a pinned spec was not refused at Stop")
	}
	refusal := refusals[len(refusals)-1]
	if got := harness.UngroundedFiles(refusal); got != "SPEC.md" {
		t.Fatalf("the refusal names %q as not grounded, want only SPEC.md:\n%s", got, refusal)
	}

	// What is left to refuse is pinned-invariant (the marker's pinned text changed with the
	// spec), a different rule: pinned-spec-holds is grounded.
	if strings.Contains(refusal, "reset --soft") {
		t.Fatalf("the refusal suggests squashing the range:\n%s", refusal)
	}
	e.Run(proj, sess, "go on", Turns("done",
		Bash("touch", `python3 -c "import pathlib; p=pathlib.Path('SPEC.md'); p.write_text(p.read_text().replace('goodwill refunds.', 'goodwill refunds (reviewed by hand).'))"`),
		harness.RefusalCommand(t, "fix", refusal, "git add", changeRuleAsk),
	))
	refusals = e.AllBlockingErrorsFrom(proj, sess, "Stop")
	if latest := refusals[len(refusals)-1]; strings.Contains(latest, `file-guard "pinned-spec-holds"`) {
		t.Fatalf("the refusal's own command did not ground the spec change:\n%s", latest)
	}
}
