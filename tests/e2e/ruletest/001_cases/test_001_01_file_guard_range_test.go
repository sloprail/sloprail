package e2e

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

const noTodoCheck = `#!/usr/bin/env bash
set -uo pipefail
payload="$(cat)"
n="$(printf '%s' "$payload" | jq -r '.changeset.files | length')" || { echo '{"reason":"unreadable changeset"}'; exit 1; }
i=0
while [ "$i" -lt "$n" ]; do
  path="$(printf '%s' "$payload" | jq -r --argjson i "$i" '.changeset.files[$i].path')"
  body="$(printf '%s' "$payload" | jq -r --argjson i "$i" '.changeset.files[$i].newContent')"
  i=$((i + 1))
  case "$body" in
    *TODO*) jq -n --arg p "$path" '{reason: ($p + " still has a TODO: finish it or file an issue")}'; exit 1 ;;
  esac
done
exit 0
`

func noTodoRule(p *harness.RuleProject) {
	rule(p, "file-guard", "no-todo", "match: docs/**\nchecks:\n  - script: ./check.sh\n", map[string]string{"check.sh": noTodoCheck})
}

// T001_01: a file-guard case judges base..HEAD through the real `sr-checks run` path: the
// refuse case sees the rule's own reason, the permit case sees it let a clean change through.
func TestT001_01_FileGuardRangeRefusesAndPermits(t *testing.T) {
	p := harness.NewRuleProject(t)
	noTodoRule(p)
	kase(p, "file-guard", "no-todo", "refuses-a-todo",
		"description: a TODO in a doc is refused\nexpect: refuse\nreason_contains: still has a TODO\n",
		"set -e\nmkdir docs\necho 'TODO write this' > docs/a.md\ngit add -A\ngit commit -q -m doc\n", "")
	kase(p, "file-guard", "no-todo", "permits-a-finished-doc",
		"expect: permit\n",
		"set -e\nmkdir docs\necho 'done' > docs/a.md\ngit add -A\ngit commit -q -m doc\n", "")

	res := p.Test()
	require.Equal(t, 0, res.Code, res.Output)
	require.Contains(t, res.Output, "PASS permits-a-finished-doc")
	require.Contains(t, res.Output, "PASS refuses-a-todo")
	require.Contains(t, res.Output, "still has a TODO")
	require.Contains(t, res.Output, "2 case(s) run, 0 failed")
}

// T001_02: a case whose expectation the rule does not meet FAILS, saying what the engine did
// instead; the exit code is 1.
func TestT001_02_AWrongExpectationFailsTheCase(t *testing.T) {
	p := harness.NewRuleProject(t)
	noTodoRule(p)
	kase(p, "file-guard", "no-todo", "claims-a-clean-doc-is-refused",
		"expect: refuse\n",
		"set -e\nmkdir docs\necho 'done' > docs/a.md\ngit add -A\ngit commit -q -m doc\n", "")
	kase(p, "file-guard", "no-todo", "claims-a-todo-passes",
		"expect: permit\n",
		"set -e\nmkdir docs\necho 'TODO' > docs/a.md\ngit add -A\ngit commit -q -m doc\n", "")
	kase(p, "file-guard", "no-todo", "wrong-reason",
		"expect: refuse\nreason_contains: nothing like this\n",
		"set -e\nmkdir docs\necho 'TODO' > docs/a.md\ngit add -A\ngit commit -q -m doc\n", "")

	res := p.Test()
	require.Equal(t, 1, res.Code, res.Output)
	require.Contains(t, res.Output, "FAIL claims-a-clean-doc-is-refused")
	require.Contains(t, res.Output, "expected the engine to refuse, but it permitted")
	require.Contains(t, res.Output, "expected the engine to permit, but it refused")
	require.Contains(t, res.Output, `does not contain "nothing like this"`)
	require.Contains(t, res.Output, "3 case(s) run, 3 failed")
}

// T001_03: `base:` names the range's start when the case does not tag one; a `base` tag made
// by setup.sh is honoured by default.
func TestT001_03_TheRangeStartsAtTheBaseTagOrHEADMinusOne(t *testing.T) {
	p := harness.NewRuleProject(t)
	noTodoRule(p)
	// the TODO is committed BEFORE the base tag: it is not part of the range
	kase(p, "file-guard", "no-todo", "a-todo-before-the-base-is-not-the-ranges",
		"expect: permit\n",
		"set -e\nmkdir docs\necho 'TODO' > docs/old.md\ngit add -A\ngit commit -q -m old\ngit tag base\necho 'fine' > docs/new.md\ngit add -A\ngit commit -q -m new\n", "")
	kase(p, "file-guard", "no-todo", "a-todo-after-the-base-is",
		"expect: refuse\nbase: rules\n",
		"set -e\nmkdir docs\necho 'TODO' > docs/old.md\ngit add -A\ngit commit -q -m old\n", "")

	res := p.Test()
	require.Equal(t, 0, res.Code, res.Output)
}

// T001_04: a setup.sh that fails fails the case, with what it printed.
func TestT001_04_AFailingSetupFailsTheCase(t *testing.T) {
	p := harness.NewRuleProject(t)
	noTodoRule(p)
	kase(p, "file-guard", "no-todo", "broken-setup", "expect: permit\n", "echo building >&2\nexit 3\n", "")

	res := p.Test()
	require.Equal(t, 1, res.Code, res.Output)
	require.Contains(t, res.Output, "setup.sh failed")
	require.Contains(t, res.Output, "building")
}

// T001_05: a case that cannot be read is reported, never skipped: a typo'd key must not read as
// a rule with fewer tests.
func TestT001_05_AnUnreadableCaseIsReported(t *testing.T) {
	p := harness.NewRuleProject(t)
	noTodoRule(p)
	kase(p, "file-guard", "no-todo", "typo", "exepct: refuse\n", "true\n", "")
	kase(p, "file-guard", "no-todo", "expects-nothing", "description: nothing\n", "true\n", "")

	res := p.Test()
	require.Equal(t, 1, res.Code, res.Output)
	require.Contains(t, res.Output, "exepct")
	require.True(t, strings.Contains(res.Output, "expects nothing"), res.Output)
}

// T001_06: naming a rule that does not exist is an error listing the rules there are; naming an
// untested rule fails (nothing proves it), while listing every rule does not.
func TestT001_06_SelectingRules(t *testing.T) {
	p := harness.NewRuleProject(t)
	noTodoRule(p)
	rule(p, "gate", "untested", "on:\n  - event: Stop\nchecks:\n  - script: ./c.sh\n", map[string]string{"c.sh": "#!/usr/bin/env bash\nexit 0\n"})
	kase(p, "file-guard", "no-todo", "permits", "expect: permit\n",
		"set -e\nmkdir docs\necho ok > docs/a.md\ngit add -A\ngit commit -q -m doc\n", "")

	res := p.Test()
	require.Equal(t, 0, res.Code, res.Output)
	require.Contains(t, res.Output, "gate/untested  no cases")

	res = p.Test("untested")
	require.Equal(t, 1, res.Code, res.Output)

	res = p.Test("nope")
	require.Equal(t, 1, res.Code, res.Output)
	require.Contains(t, res.Output, "no rule matches")
	require.Contains(t, res.Output, "file-guard/no-todo")
}
