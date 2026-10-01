package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The registry is the whole enforcement: verify-scanner-coverage holds every
// search to what scanner-declared logged, and search-needs-declared-scanner lets
// a search run only once something is logged. So each way the registry could
// shrink, merge, stick, or be misread is a way past both gates. Each test here is
// one such way, found by review of the shipped example and reproduced first.

// researchProjectWithScanner is researchProject with activeScanner (guardrail,
// llm, agent) committed at scanners/mine — a scanner that predates the session.
func researchProjectWithScanner(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e, proj := researchProject(t)
	e.WriteFile(proj, "scanners/mine/scanner.yaml", activeScanner)
	e.CommitAll(proj, "scanner")
	return e, proj
}

// stopRefusals is every coverage-gate refusal text the session's Stops got.
func stopRefusals(e *harness.Env, proj, sess string) string {
	return strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
}

// T038_27: two scanners that share a folder name are two obligations. The
// registry keyed a scanner by its folder's LAST name, so scanners/mine and
// zz/scanners/mine were one entry and the later write replaced the earlier's
// keywords — here with a narrower set, created fresh so nothing asked for a
// citation — and a search covering only that narrower set passed Stop.
func TestT038_27_ScannersSharingAFolderNameAreTwoObligations(t *testing.T) {
	e, proj := researchProject(t)
	const sess = "s-038-27"
	e.Run(proj, sess, "research guardrails", Turns("done",
		Write("w1", "scanners/mine/scanner.yaml", activeScanner),
		Write("w2", "zz/scanners/mine/scanner.yaml", "active: true\nkeywords:\n  - guardrail\n"),
		Bash("b1", stubbed(`gh search repos guardrail`)),
	).ThenCommit("write the files"))

	reg := e.GuardrailState(proj, sess, "scanner-declared", "scanner:")
	if len(reg) != 2 {
		t.Errorf("the registry should hold both scanners, keyed by their folder: %v", reg)
	}
	joined := stopRefusals(e, proj, sess)
	if !strings.Contains(joined, coverageRefusal) {
		t.Fatalf("Stop was not refused although scanners/mine's llm and agent were never covered:\n%s", joined)
	}
	if !strings.Contains(joined, `scanners/mine: "guardrail" "llm" "agent"`) {
		t.Errorf("the refusal does not name the uncovered scanner by its path:\n%s", joined)
	}
	if strings.Contains(joined, "zz/scanners/mine:") {
		t.Errorf("the covered scanner was named uncovered:\n%s", joined)
	}
}

// T038_28: a delete the user asked for — cited, and judged to be what they
// asked — retires the scanner's obligation. Before, a scanner declared this
// session stayed in the registry forever and every later Stop was refused,
// telling the agent to search for a scanner the user had removed. Declaring it
// again makes it owed again. (T038_24 pins the other side: a delete no rule saw
// retires nothing.)
func TestT038_28_ACitedDeleteRetiresTheObligation(t *testing.T) {
	e, proj := researchProject(t)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "the user asked to remove the scanner"}`)
	const sess = "s-038-28"
	const ask = "remove the mine scanner, we no longer track it"

	// A scanner written and deleted inside one range is no change at all, so the
	// declaration is committed first; the delete is then its own changeset, whose
	// commit carries the user's words as a trailer.
	e.Run(proj, sess, ask, Turns("done",
		Write("w1", "scanners/mine/scanner.yaml", activeScanner),
	).ThenCommit("declare the scanner"))
	// The declaring Stop was owed a search, rightly; what counts is what comes after.
	before := coverageRefusals(t, e.TranscriptPath(proj, sess))
	if before == 0 {
		t.Fatalf("precondition: the declared scanner should be owed a search")
	}
	res := e.Run(proj, sess, "go ahead", Turns("done",
		Bash("b1", "sr-session trajectory cite '"+ask+"' --source-types user && rm -rf scanners/mine"),
	).ThenCommit("remove the scanner", harness.CitesUser(ask)))
	if res.Refused() {
		t.Fatalf("the delete the user asked for was refused:\n%s", res.Output)
	}
	if _, err := os.Stat(filepath.Join(proj, "scanners", "mine", "scanner.yaml")); !os.IsNotExist(err) {
		t.Fatalf("precondition: the cited delete should have landed: %v", err)
	}
	if n := coverageRefusals(t, e.TranscriptPath(proj, sess)) - before; n != 0 {
		t.Fatalf("Stop was refused %d time(s) for a scanner the user asked to remove:\n%s", n, stopRefusals(e, proj, sess))
	}

	// Declared again, it is a new obligation — the retirement was of the
	// declaration the user removed, not of the name.
	retired := coverageRefusals(t, e.TranscriptPath(proj, sess))
	e.Run(proj, sess, "declare it again", Turns("done",
		Write("w2", "scanners/mine/scanner.yaml", activeScanner),
	).ThenCommit("write the files"))
	if n := coverageRefusals(t, e.TranscriptPath(proj, sess)) - retired; n == 0 {
		t.Errorf("a scanner declared again after its retirement was not owed a search")
	}
}

// T038_29: a registry that cannot be read refuses — it does not permit. Both
// gates read it with `sr-session state list`; when that failed, the search gate
// permitted ("plumbing fails open") and the coverage gate saw `[]` (jq -s of
// nothing) and passed Stop. Here `state list` exits 1 while every other
// sr-session call works, so the context still logs the scanner.
func TestT038_29_AnUnreadableRegistryRefuses(t *testing.T) {
	e, proj := researchProject(t)
	e.WrapBinary("sr-session", `if [ "$1" = "state" ] && [ "$2" = "list" ]; then echo "state list: database is locked" >&2; exit 1; fi`)
	const sess = "s-038-29"

	res := e.Run(proj, sess, "research guardrails", Turns("done",
		Write("w1", "scanners/mine/scanner.yaml", activeScanner),
		Bash("b1", stubbed(`gh search repos guardrail llm agent`)),
	).ThenCommit("write the files"))
	if !res.Refused() || !res.Saw("could not be checked against a declared scanner") {
		t.Errorf("a search was let through although the registry could not be read:\n%s", res.Output)
	}
	if res.Saw("stub-gh search") {
		t.Errorf("the unchecked search ran:\n%s", res.Output)
	}
	if joined := stopRefusals(e, proj, sess); !strings.Contains(joined, "verify-scanner-coverage could not check this turn") {
		t.Errorf("Stop passed although the registry could not be read:\n%s", joined)
	}
}

// T038_30: a refused narrowing write never shrinks the obligation. The context
// enters before the keywords-hold gate refuses, so its Pre log must not narrow
// the entry. It took the union with what was LOGGED only — so for a committed
// scanner this session never logged, a refused write dropping `agent` logged
// the narrowed set, no Post event followed (the file never changed), and a
// search without `agent` passed Stop. Both variants: the scanner registered
// first by an unchanged rewrite (pins the logged-union), and not registered.
func TestT038_30_ARefusedNarrowingNeverShrinksTheObligation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		turns []harness.Turn
	}{
		{"registered by an unchanged rewrite first", []harness.Turn{
			Write("w0", "scanners/mine/scanner.yaml", activeScanner),
			Write("w1", "scanners/mine/scanner.yaml", narrowedScanner),
			Bash("b1", stubbed(`gh search repos guardrail llm`)),
		}},
		{"never registered", []harness.Turn{
			Write("w1", "scanners/mine/scanner.yaml", narrowedScanner),
			Bash("b1", stubbed(`gh search repos guardrail llm`)),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, proj := researchProjectWithScanner(t)
			e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
			const sess = "s-038-30"
			res := e.Run(proj, sess, "search for guardrail work", Turns("done", tc.turns...))
			if !res.Saw("drops the declared keyword(s) agent") {
				t.Fatalf("precondition: the narrowing write should have been refused:\n%s", res.Output)
			}
			if got := readScanner(t, proj); got != activeScanner {
				t.Fatalf("precondition: the refused write reached the file:\n%s", got)
			}
			if joined := stopRefusals(e, proj, sess); !strings.Contains(joined, `scanners/mine: "agent" "guardrail" "llm"`) &&
				!strings.Contains(joined, `scanners/mine: "guardrail" "llm" "agent"`) {
				t.Errorf("Stop passed on a search without `agent`, which the scanner still declares:\n%s", joined)
			}
		})
	}
}

// T038_31: the guard reads a scanner's keywords exactly as the registry does.
// They were two parsers: the guard's stopped at a column-0 comment the
// registry's read past, so a keyword after one could be dropped with no
// citation (and, the write admitted, the settled file then narrowed the
// registry); and only the registry's stripped quotes, so re-quoting the same
// keywords read as dropping all of them.
func TestT038_31_TheGuardReadsKeywordsAsTheRegistryDoes(t *testing.T) {
	t.Run("a keyword after a column-0 comment cannot be dropped uncited", func(t *testing.T) {
		e, proj := researchProject(t)
		e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
		const sess = "s-038-31a"
		const commented = "active: true\nkeywords:\n  - guardrail\n# the rest\n  - agent\n"
		res := e.Run(proj, sess, "search for guardrail work", Turns("done",
			Write("w1", "scanners/mine/scanner.yaml", commented),
			Write("w2", "scanners/mine/scanner.yaml", "active: true\nkeywords:\n  - guardrail\n# the rest\n"),
		).ThenCommit("write the files"))
		if !res.Refused() || !res.Saw("drops the declared keyword(s) agent") {
			t.Errorf("dropping the keyword after the comment was not refused:\n%s", res.Output)
		}
		if got := e.GuardrailState(proj, sess, "scanner-declared", "")["scanner:scanners/mine"]; !strings.Contains(got, "agent") {
			t.Errorf("the registry lost `agent`: %q", got)
		}
	})

	t.Run("re-quoting the same keywords drops nothing", func(t *testing.T) {
		e, proj := keywordsProject(t)
		e.InstallJudgeClaude(`{"pass": false, "reasoning": "SR038 the judge ran on a restyle"}`)
		res := e.Run(proj, "s-038-31b", "tidy the scanner", Turns("done",
			Write("w1", "scanners/mine/scanner.yaml", "active: true\nkeywords:\n  - \"guardrail\"\n  - 'llm'\n  - agent   # the agent keyword\n"),
		).ThenCommit("write the files"))
		if res.Refused() || res.Saw("SR038 the judge ran") {
			t.Errorf("a quote-only restyle was refused or judged as a drop:\n%s", res.Output)
		}
	})
}
