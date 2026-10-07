package e2e

import (
	"strings"
	"testing"
)

// T043_01: a failing case refuses the change, naming the case (its owner and name), its status and its output.
func TestT043_01_FailingCaseRefused(t *testing.T) {
	got := refusalOf(t, notes(map[string]string{"broken": failCase}))
	for _, want := range []string{"gate/notes:broken: fail", "the broken one"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the refusal lacks %q:\n%s", want, got)
		}
	}
}

// T043_02: the same change with a passing case is not refused; a case is all it has to hold.
func TestT043_02_PassingCaseAllowed(t *testing.T) {
	if got := refusalOf(t, notes(map[string]string{"fine": passCase})); got != "" {
		t.Fatalf("a passing case was refused:\n%s", got)
	}
}

// T043_03: a new rule no case lives beside is refused, naming the rule and the folder a case goes in.
func TestT043_03_NewRuleWithoutCaseRefused(t *testing.T) {
	got := refusalOf(t, notes(nil))
	if !strings.Contains(got, "no sr-test case lives in the folder") || !strings.Contains(got, "gate/notes") ||
		!strings.Contains(got, ".sloprail/gate/notes/tests/<case>/") {
		t.Fatalf("a new rule without a case was not refused by name:\n%s", got)
	}
}

// T043_04: a rule nobody touched is not refused for having no case (legacy pass): the base holds the gate
// "old" with no case, the range adds a covered gate beside it.
func TestT043_04_UntouchedRuleIsNotRefused(t *testing.T) {
	e, proj := scopeEnv(t)
	for path, body := range map[string]string{
		".sloprail/gate/fresh/gate.yaml":             gateYAML,
		".sloprail/gate/fresh/check.sh":              checkSh,
		".sloprail/gate/fresh/tests/fine/test.sh":    passCase,
		".sloprail/gate/notes/tests/broken/test.sh":  passCase, // the base's broken case, fixed in the range
		".sloprail/gate/notes/tests/another/test.sh": passCase,
	} {
		e.WriteExecutable(proj, path, body)
	}
	e.CommitAll(proj, "add a covered gate")
	got := strings.Join(e.CheckRunRange(proj, "s-043", "origin/main", "HEAD"), "\n")
	if got != "" {
		t.Fatalf("a change that did not touch the legacy rule was refused:\n%s", got)
	}
}

// T043_05: a change to a case judges its rule alone: every case of that rule runs (the base's broken sibling is
// refused, named; the edited good one is not), and no case of another rule does.
func TestT043_05_CaseChangeRunsItsRuleOnly(t *testing.T) {
	e, proj := scopeEnv(t)
	e.WriteExecutable(proj, ".sloprail/gate/notes/tests/good/test.sh", passCase+"# a note\n")
	e.CommitAll(proj, "edit the good case")
	got := strings.Join(e.CheckRunRange(proj, "s-043", "origin/main", "HEAD"), "\n")
	if !strings.Contains(got, "gate/notes:broken: fail") || strings.Contains(got, "gate/notes:good") || !strings.Contains(got, "--rule gate/notes") {
		t.Fatalf("a case change did not judge its rule's cases, or named the wrong one:\n%s", got)
	}
}

// T043_06: a change to anything but tests (here the gate's script) runs all the cases: the broken one is
// refused, named, and the good one is not.
func TestT043_06_RuleChangeRunsAllCases(t *testing.T) {
	e, proj := scopeEnv(t)
	e.WriteExecutable(proj, ".sloprail/gate/notes/check.sh", checkSh+"# a note\n")
	e.CommitAll(proj, "edit the gate")
	got := strings.Join(e.CheckRunRange(proj, "s-043", "origin/main", "HEAD"), "\n")
	if !strings.Contains(got, "gate/notes:broken: fail") || !strings.Contains(got, "the broken one") || strings.Contains(got, "gate/notes:good") {
		t.Fatalf("a rule change did not run all the cases, or named the wrong one:\n%s", got)
	}
}

// T043_07: a deleted case is not run; deleting the last case of a rule leaves it without one and is refused.
func TestT043_07_DeletedCases(t *testing.T) {
	e, proj := scopeEnv(t)
	e.Git(proj, "rm", "-q", "-r", ".sloprail/gate/notes/tests/broken")
	e.CommitAll(proj, "delete the broken case")
	if got := strings.Join(e.CheckRunRange(proj, "s-043", "origin/main", "HEAD"), "\n"); got != "" {
		t.Fatalf("deleting a broken case was refused:\n%s", got)
	}
	e.Git(proj, "rm", "-q", "-r", ".sloprail/gate/notes/tests/good")
	e.CommitAll(proj, "delete the last case")
	got := strings.Join(e.CheckRunRange(proj, "s-043", "origin/main", "HEAD"), "\n")
	if !strings.Contains(got, "no sr-test case lives in the folder") || !strings.Contains(got, "gate/notes") {
		t.Fatalf("a rule left without a case was not refused:\n%s", got)
	}
}
