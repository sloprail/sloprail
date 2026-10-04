package e2e

import (
	"strings"
	"testing"
)

// T043_01: a failing case refuses the change, naming the case, its status and its output.
func TestT043_01_FailingCaseRefused(t *testing.T) {
	got := refusalOf(t, map[string]string{".sloprail/tests/broken/test.sh": "#!/usr/bin/env bash\necho 'the broken one' >&2\nexit 1\n"})
	for _, want := range []string{"broken: fail", "the broken one"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the refusal lacks %q:\n%s", want, got)
		}
	}
}

// T043_02: the same change with a passing case is not refused; a case is all it has to hold.
func TestT043_02_PassingCaseAllowed(t *testing.T) {
	if got := refusalOf(t, map[string]string{".sloprail/tests/fine/test.sh": "#!/usr/bin/env bash\nexit 0\n"}); got != "" {
		t.Fatalf("a passing case was refused:\n%s", got)
	}
}

// T043_03: a new rule no case fires is refused, naming the rule.
func TestT043_03_NewRuleWithoutCaseRefused(t *testing.T) {
	got := refusalOf(t, map[string]string{
		".sloprail/gate/notes/gate.yaml": gateYAML,
		".sloprail/gate/notes/check.sh":  checkSh,
		".sloprail/tests/fine/test.sh":   "#!/usr/bin/env bash\nexit 0\n",
	})
	if !strings.Contains(got, "no sr-test case exercises") || !strings.Contains(got, "gate/notes") {
		t.Fatalf("a new rule no case fires was not refused by name:\n%s", got)
	}
}

// T043_04: a change that touches no rule (only a case) is not refused for a rule nobody touched having no
// case: the legacy pass. Here the only rule is in the base and the range adds just a passing case.
func TestT043_04_UntouchedRuleIsNotRefused(t *testing.T) {
	e := newEnvWithLegacyRule(t)
	got := strings.Join(e.refusals(), "\n")
	if got != "" {
		t.Fatalf("a change that did not touch the legacy rule was refused:\n%s", got)
	}
}
