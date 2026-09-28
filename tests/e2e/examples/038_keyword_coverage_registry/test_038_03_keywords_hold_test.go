package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// scanner-keywords-hold: a declared scanner's keywords hold. Adding one needs
// nothing; dropping one needs a citation of the user's words asking for it, and a
// judge checks those words ask for THESE keywords. Found on a real Haiku run that,
// refused by the coverage gate, rewrote its keywords to fit its search.

const narrowedScanner = "active: true\nkeywords:\n  - guardrail\n  - llm\n"

// keywordsProject installs the example with activeScanner (guardrail, llm, agent)
// already declared and committed.
func keywordsProject(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.WriteFile(proj, "scanners/mine/scanner.yaml", activeScanner)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")
	return e, proj
}

// readScanner is the declared scanner as it stands on disk.
func readScanner(t *testing.T, proj string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(proj, "scanners", "mine", "scanner.yaml"))
	if err != nil {
		t.Fatalf("read the scanner: %v", err)
	}
	return string(b)
}

func srWriteScanner(id, content, quote string) harness.Turn {
	return Bash(id, "sr-file write scanners/mine/scanner.yaml --content '"+content+"' --cite:user '"+quote+"'")
}

// T038_08: dropping a declared keyword with no citation is refused before it
// lands, and the refusal carries the rule's hint naming the dropped keyword.
func TestT038_08_UncitedKeywordDropRefused(t *testing.T) {
	e, proj := keywordsProject(t)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	res := e.Run(proj, "s-038-08", "search for guardrail work", Turns("done",
		Write("w1", "scanners/mine/scanner.yaml", narrowedScanner),
	))
	if !res.Refused() {
		t.Fatalf("an uncited keyword drop was not refused:\n%s", res.Output)
	}
	if !res.Saw("must cite the user's own words (--cite:user)") || !res.Saw("drops the declared keyword(s) agent") {
		t.Errorf("the refusal does not say what to cite or name the dropped keyword:\n%s", res.Output)
	}
	if !res.Saw("sr-file edit scanners/mine/scanner.yaml") {
		t.Errorf("the refusal carries no runnable sr-file command:\n%s", res.Output)
	}
	if got := readScanner(t, proj); got != activeScanner {
		t.Errorf("the refused drop reached the file:\n%s", got)
	}
}

// T038_09: adding a keyword needs no citation, and no judge.
func TestT038_09_AddedKeywordNeedsNothing(t *testing.T) {
	e, proj := keywordsProject(t)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "SR038 the judge ran on an added keyword"}`)

	res := e.Run(proj, "s-038-09", "search for guardrail work", Turns("done",
		Write("w1", "scanners/mine/scanner.yaml", activeScanner+"  - eval\n"),
	))
	if res.Refused() || res.Saw("SR038 the judge ran") {
		t.Fatalf("adding a keyword was refused or judged:\n%s", res.Output)
	}
}

// T038_10: a drop citing the user's words asking for it lands.
func TestT038_10_CitedKeywordDropAdmits(t *testing.T) {
	e, proj := keywordsProject(t)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "the user asked to drop agent"}`)

	const ask = "drop the agent keyword from the scanner"
	res := e.Run(proj, "s-038-10", ask, Turns("done",
		srWriteScanner("b1", narrowedScanner, ask),
	))
	if res.Refused() {
		t.Fatalf("a drop the user asked for was refused:\n%s", res.Output)
	}
	if got := readScanner(t, proj); !strings.Contains(got, "llm") || strings.Contains(got, "agent") {
		t.Errorf("the cited drop did not land:\n%s", got)
	}
}

// T038_11: a drop citing words that do not ask for it is refused by the judge.
func TestT038_11_DropCitingUnrelatedWordsRefused(t *testing.T) {
	e, proj := keywordsProject(t)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "SR038 the cited words ask for a search, not to drop agent"}`)

	const ask = "search GitHub for guardrail projects"
	res := e.Run(proj, "s-038-11", ask, Turns("done",
		srWriteScanner("b1", narrowedScanner, ask),
	))
	if !res.Refused() || !res.Saw("SR038 the cited words ask for a search") {
		t.Fatalf("a drop citing unrelated words was not refused by the judge:\n%s", res.Output)
	}
}

// T038_12: a scanner declared below the root — `.claude/scanners/<name>/`, where
// a real Haiku run put one — is still a scanner: the context logs it and the
// coverage gate refuses it uncovered, so a misplaced declaration cannot escape.
func TestT038_12_NestedScannerIsStillChecked(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-038-12"
	e.Run(proj, sess, "declare a scanner but never search", Turns("done",
		Write("w1", ".claude/scanners/mine/scanner.yaml", activeScanner),
	))
	if _, ok := e.GuardrailState(proj, sess, "scanner-declared", "")["scanner:.claude/scanners/mine"]; !ok {
		t.Fatalf("a scanner declared under .claude/scanners/ was not logged")
	}
	if !strings.Contains(strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n"), coverageRefusal) {
		t.Errorf("the coverage gate did not refuse an uncovered nested scanner")
	}
}
