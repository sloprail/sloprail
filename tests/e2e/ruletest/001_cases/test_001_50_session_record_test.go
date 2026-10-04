package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T001_50: `user_says` puts the user's messages in the session record, and the engine's own
// citation machinery resolves against them: a commit trailer for a file-guard that requires a
// citation, a chained cite for a gate. A quote nobody said grounds nothing.
func TestT001_50_CitationsResolveAgainstWhatTheUserSaid(t *testing.T) {
	p := harness.NewRuleProject(t)
	rule(p, "file-guard", "docs-need-an-ask", "match: docs/**\nrequire:\n  - citation: {source_types: [user]}\n", nil)
	cited := "set -e\necho hi > README.md\ngit add -A\ngit commit -q -m base\ngit tag base\nmkdir docs\necho onboarding > docs/a.md\ngit add -A\ngit commit -q -m 'add the doc' -m 'Sloprail-Cites-User: %s'\n"
	kase(p, "file-guard", "docs-need-an-ask", "cited", "user_says: [\"please add a docs page about onboarding\"]\nexpect: permit\n",
		sprintf(cited, "add a docs page about onboarding"), "")
	kase(p, "file-guard", "docs-need-an-ask", "quote-nobody-said", "user_says: [\"please add a docs page about onboarding\"]\nexpect: refuse\n",
		sprintf(cited, "delete everything"), "")

	rule(p, "gate", "push-needs-an-ask",
		"on:\n  - event: PreCommandInvoke\n    match: any(event.invocations, .bin == \"git\" and \"push\" in .argv)\nrequire:\n  - citation: {source_types: [user]}\n", nil)
	kase(p, "gate", "push-needs-an-ask", "push", "user_says: [\"please push the branch\"]\n", baseSetup, `- kind: PreCommandInvoke
  command: git push origin main
  expect: refuse
  reason_contains: cite
- kind: PreCommandInvoke
  command: sr-session trajectory cite 'please push the branch' && git push origin main
  expect: permit
- kind: PreCommandInvoke
  command: sr-session trajectory cite 'something nobody said' && git push origin main
  expect: refuse
`)
	res := p.Test()
	require.Equal(t, 0, res.Code, res.Output)
	require.Contains(t, res.Output, "3 case(s) run, 0 failed")
}

// T001_51: every Pre event is written into the session's record before its hook runs, as a
// harness writes a tool call, so a rule asking what the agent did sees it: a Skill tool use
// satisfies `require: skill`, and a Read of a skill's page satisfies `files:`.
func TestT001_51_ThePreEventsAreInTheSessionRecord(t *testing.T) {
	p := harness.NewRuleProject(t)
	rule(p, "gate", "style-before-docs",
		"on:\n  - event: PreFileWrite\n    match: event.path startsWith \"docs/\"\nrequire:\n  - skill: house-style\n    files: [voice.md]\n", nil)
	kase(p, "gate", "style-before-docs", "needs-the-skill-and-the-page",
		"description: the skill, then its page, then the write\n",
		"set -e\nmkdir -p .claude/skills/house-style\nprintf -- '---\\nname: house-style\\ndescription: voice\\n---\\n# Style\\n' > .claude/skills/house-style/SKILL.md\necho voice > .claude/skills/house-style/voice.md\ngit add -A\ngit commit -q -m skill\n",
		`- kind: PreFileCreate
  path: docs/a.md
  newContent: "x\n"
  expect: refuse
  reason_contains: house-style
- kind: PreToolUse
  tool: Skill
  input: {skill: house-style}
- kind: PreFileCreate
  path: docs/a.md
  newContent: "x\n"
  expect: refuse
  reason_contains: voice.md
- kind: PreToolUse
  tool: Read
  input: {file_path: .claude/skills/house-style/voice.md}
- kind: PreFileCreate
  path: docs/a.md
  newContent: "x\n"
  expect: permit
- kind: PreFileCreate
  path: notes/b.md
  newContent: "unrelated\n"
  expect: permit
`)
	res := p.Test()
	require.Equal(t, 0, res.Code, res.Output)
}
