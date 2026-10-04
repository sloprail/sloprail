package e2e

import (
	"strings"
	"testing"
)

const goodTail = `git init -q .
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt x)
echo "$RESULT" | jq -e '[.events[]|select(.rule=="demo")] | .[0].outcome=="refused" and (.[0].reason|contains("read-only"))' >/dev/null
`

// T042_01: a case with no shebang is refused, saying where the shebang goes.
func TestT042_01_NoShebangRefused(t *testing.T) {
	got := refusalOf(t, ".sloprail/tests/c", goodTail)
	if !strings.Contains(got, "does not start with a shebang") || !strings.Contains(got, ".sloprail/tests/c") {
		t.Fatalf("a case without a shebang was not refused for it:\n%s", got)
	}
}

// T042_02: a case that asserts only the agent's exit code never asserts the events.
func TestT042_02_NoEventsAssertedRefused(t *testing.T) {
	got := refusalOf(t, ".sloprail/tests/c", "#!/usr/bin/env bash\nRESULT=$(sr-test agent a.sh)\necho \"$RESULT\" | jq -e '.exit == 0'\n")
	if !strings.Contains(got, "never asserts on the events") {
		t.Fatalf("a case with no event assertion was not refused for it:\n%s", got)
	}
}

// T042_03: a refusal asserted without its reason is refused.
func TestT042_03_RefusalWithoutReasonRefused(t *testing.T) {
	got := refusalOf(t, ".sloprail/tests/c", "#!/usr/bin/env bash\nRESULT=$(sr-test agent a.sh)\necho \"$RESULT\" | jq -e '[.events[]|select(.rule==\"demo\")] | .[0].outcome==\"refused\"'\n")
	if !strings.Contains(got, "never checks its reason") {
		t.Fatalf("a refusal asserted without its reason was not refused for it:\n%s", got)
	}
}

// T042_04: swallowing a failure and asserting a constant are each refused.
func TestT042_04_VacuousAssertionsRefused(t *testing.T) {
	got := refusalOf(t, ".sloprail/tests/c", "#!/usr/bin/env bash\n"+strings.TrimPrefix(goodTail, "git init -q .\n")+"jq -e '.' <<<'{}'\n[ 1 ]\ntrue\nfalse || true\n")
	for _, want := range []string{"'|| true'", "constant", "only 'true'"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the refusal lacks %q:\n%s", want, got)
		}
	}
}

// T042_05: the case that fixes the one shape passes the floor: the only refusals left are none from the
// script (a judge, unmocked, would run next; the harness error is not a script reason).
func TestT042_05_FixedCaseNotRefusedByTheFloor(t *testing.T) {
	got := refusalOf(t, ".sloprail/tests/c", "#!/usr/bin/env bash\nset -euo pipefail\n"+goodTail)
	for _, floor := range []string{"shebang", "never asserts", "never checks its reason", "'|| true'", "constant"} {
		if strings.Contains(got, floor) {
			t.Fatalf("a rigorous-looking case was refused by the script floor for %q:\n%s", floor, got)
		}
	}
}
