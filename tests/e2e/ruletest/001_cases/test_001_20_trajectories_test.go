package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The research run: a context a #research tag opens, and a Stop gate that reads it.

const researchEnter = `#!/usr/bin/env bash
set -uo pipefail
cat >/dev/null
sr-session state set "tag:research" "declared"
jq -n '{declared: true}'
`

const researchExit = `#!/usr/bin/env bash
set -uo pipefail
cat >/dev/null
[ -f "${SR_WORKSPACE:-.}/notes/research.md" ] && exit 0
exit 1
`

const needsNoteCheck = `#!/usr/bin/env bash
set -uo pipefail
cat >/dev/null
entries="$(sr-session state list --owner research-run 2>/dev/null)" || entries=""
tags="$(printf '%s' "$entries" | jq -s -r '[.[] | select(.key | startswith("tag:"))] | length')" || tags=0
[ "${tags:-0}" -gt 0 ] || exit 0
[ -f "${SR_WORKSPACE:-.}/notes/research.md" ] && exit 0
jq -n '{reason: "a #research run needs notes/research.md before the turn ends"}'
exit 1
`

func researchRules(p *harness.RuleProject) {
	rule(p, "context", "research-run",
		"on:\n  - event: PostTagWrite\n    match: any(event.tags, .label == \"research\")\nenter: ./enter.sh\nexit: ./exit.sh\n",
		map[string]string{"enter.sh": researchEnter, "exit.sh": researchExit})
	rule(p, "gate", "research-needs-note",
		"on:\n  - event: Stop\n    match: context[\"research-run\"].active\nrequire:\n  - context: research-run\nchecks:\n  - script: ./check.sh\n",
		map[string]string{"check.sh": needsNoteCheck})
}

// T001_20: a context is activated by an event, persists across the engine's processes in the
// case's own state, and is read by a gate through `context[...]` and `require`: the Stop is
// refused with the gate's reason, and the context stays open for the next cycle.
func TestT001_20_AContextActivatedByAnEventIsReadByAGate(t *testing.T) {
	p := harness.NewRuleProject(t)
	researchRules(p)
	kase(p, "gate", "research-needs-note", "refuses-a-research-run-with-no-note",
		"with: [context/research-run]\nexpect: refuse\nreason_contains: notes/research.md\ncontexts:\n  research-run: active\n",
		baseSetup, "- kind: PostTagWrite\n  tags: [research]\n- kind: Stop\n")
	kase(p, "gate", "research-needs-note", "permits-when-the-note-landed",
		"with: [context/research-run]\nexpect: permit\ncontexts:\n  research-run: inactive\n",
		baseSetup, "- kind: PostTagWrite\n  tags: [research]\n- run: |\n    mkdir notes\n    echo findings > notes/research.md\n- kind: Stop\n")
	kase(p, "gate", "research-needs-note", "permits-a-turn-with-no-research",
		"with: [context/research-run]\nexpect: permit\ncontexts:\n  research-run: inactive\n",
		baseSetup, "- kind: Stop\n")

	res := p.Test("research-needs-note")
	require.Equal(t, 0, res.Code, res.Output)
	require.Contains(t, res.Output, "PASS refuses-a-research-run-with-no-note")
	require.Contains(t, res.Output, "a #research run needs notes/research.md")
	require.Contains(t, res.Output, "3 case(s) run, 0 failed")
}

// T001_21: the context's own rule is tested by the states it ends a trajectory in.
func TestT001_21_ContextCasesAssertActiveAndInactive(t *testing.T) {
	p := harness.NewRuleProject(t)
	researchRules(p)
	kase(p, "context", "research-run", "a-tag-opens-it",
		"contexts:\n  research-run: active\n",
		baseSetup, "- kind: PostTagWrite\n  tags: [research]\n- kind: Stop\n")
	kase(p, "context", "research-run", "an-unrelated-tag-does-not",
		"contexts:\n  research-run: inactive\n",
		baseSetup, "- kind: PostTagWrite\n  tags: [lunch]\n- kind: Stop\n")
	kase(p, "context", "research-run", "the-note-closes-it",
		"contexts:\n  research-run: inactive\n",
		baseSetup, "- kind: PostTagWrite\n  tags: [research]\n- run: |\n    mkdir notes\n    echo findings > notes/research.md\n- kind: Stop\n")

	res := p.Test("context/research-run")
	require.Equal(t, 0, res.Code, res.Output)
	require.Contains(t, res.Output, "3 case(s) run, 0 failed")

	// the same case claiming the wrong state fails, naming the context
	kase(p, "context", "research-run", "wrong-claim",
		"contexts:\n  research-run: active\n",
		baseSetup, "- kind: PostTagWrite\n  tags: [lunch]\n- kind: Stop\n")
	res = p.Test("context/research-run")
	require.Equal(t, 1, res.Code, res.Output)
	require.Contains(t, res.Output, `context "research-run" is inactive, expected active`)
}

// T001_22: a sub-agent's events go through the engine's sub-agent hooks: its own Stop is its
// own cycle, judged by its own session (its own contexts and state), and a refusal there is
// the sub-agent's.
func TestT001_22_ASubAgentIsItsOwnSession(t *testing.T) {
	p := harness.NewRuleProject(t)
	researchRules(p)
	kase(p, "gate", "research-needs-note", "a-scout-that-researched-needs-a-note",
		"with: [context/research-run]\nexpect: refuse\nreason_contains: notes/research.md\n",
		baseSetup, "- subagent: start\n  id: scout\n- kind: PostTagWrite\n  agent: scout\n  tags: [research]\n- subagent: stop\n  id: scout\n")
	kase(p, "gate", "research-needs-note", "the-roots-research-is-not-the-scouts",
		"with: [context/research-run]\n",
		baseSetup, `- kind: PostTagWrite
  tags: [research]
- subagent: start
  id: scout
- subagent: stop
  id: scout
  expect: permit
- kind: Stop
  expect: refuse
`)

	res := p.Test("research-needs-note")
	require.Equal(t, 0, res.Code, res.Output)
	require.Contains(t, res.Output, "2 case(s) run, 0 failed")
}

// T001_23: the composed trajectory of the docs: a context, a gate and a sub-agent over one
// session. The root declares research; its Stop is refused with no note; a sub-agent writes
// the note and finishes (its own Stop passes); the root's retried Stop then passes and the
// context closes.
func TestT001_23_ContextGateAndSubAgentComposed(t *testing.T) {
	p := harness.NewRuleProject(t)
	researchRules(p)
	kase(p, "gate", "research-needs-note", "a-scout-writes-the-note",
		"description: the root's refused Stop is satisfied by a sub-agent's note\nwith: [context/research-run]\n",
		baseSetup, `- kind: PostTagWrite
  tags: [research]
- kind: Stop
  expect: refuse
  reason_contains: notes/research.md
  contexts: {research-run: active}
- subagent: start
  id: scout
- run: |
    mkdir notes
    echo findings > notes/research.md
- subagent: stop
  id: scout
  expect: permit
- kind: Stop
  expect: permit
  contexts: {research-run: inactive}
`)
	res := p.Test("research-needs-note")
	require.Equal(t, 0, res.Code, res.Output)
	require.Contains(t, res.Output, "PASS a-scout-writes-the-note")
}

// T001_24: Pre events are dispatched the way a harness asks before a tool call: a gate on
// PreFileWrite sees the bytes, the markers derived from them and `resultKnown`; one on
// PreCommandInvoke sees the command parsed by the engine's own parser.
func TestT001_24_PreEventsReachGates(t *testing.T) {
	p := harness.NewRuleProject(t)
	rule(p, "gate", "no-secrets",
		"on:\n  - event: PreFileWrite\n    match: event.resultKnown and event.newContent contains \"API_KEY=\"\n  - event: PreFileWrite\n    match: not event.resultKnown\nchecks:\n  - script: ./no.sh\n",
		map[string]string{"no.sh": refusingScript("do not write secrets, and write files whole so they can be checked")})
	kase(p, "gate", "no-secrets", "refuses-a-secret",
		"description: a key in the bytes, and a write the engine could not compute\n",
		baseSetup, `- kind: PreFileWrite
  path: config.env
  newContent: "API_KEY=abc\n"
  expect: refuse
  reason_contains: secrets
- kind: PreFileWrite
  path: README.md
  resultKnown: false
  expect: refuse
`)
	kase(p, "gate", "no-secrets", "permits-ordinary-writes",
		"description: PreFileWrite becomes an update for a file that exists, a create for one that does not\n",
		baseSetup, `- kind: PreFileWrite
  path: README.md
  newContent: "hello again\n"
  expect: permit
- kind: PreFileWrite
  path: notes.md
  newContent: "notes\n"
  expect: permit
`)

	res := p.Test("no-secrets")
	require.Equal(t, 0, res.Code, res.Output)
	require.Contains(t, res.Output, "2 case(s) run, 0 failed")
}

// T001_25: a gate that matches a command's parsed invocations, nested one level deep.
func TestT001_25_CommandsAreParsedByTheEnginesOwnParser(t *testing.T) {
	p := harness.NewRuleProject(t)
	rule(p, "gate", "no-curl",
		"on:\n  - event: PreCommandInvoke\n    match: any(event.invocations, .bin == \"curl\")\nchecks:\n  - script: ./no.sh\n",
		map[string]string{"no.sh": refusingScript("use the fetch tool")})
	kase(p, "gate", "no-curl", "sees-through-nesting", "description: x\n", baseSetup, `- kind: PreCommandInvoke
  command: echo go && (cd /tmp && sudo curl -s https://example.com | head)
  expect: refuse
- kind: PreCommandInvoke
  command: ls -la
  expect: permit
`)
	res := p.Test("no-curl")
	require.Equal(t, 0, res.Code, res.Output)
}

// T001_26: a Post file event (here the PostFileWrite alias) is delivered with the next Stop and
// carries the settled file; a context that wakes on it enters, and stays open while its exit
// declines.
func TestT001_26_APostFileEventWakesAContext(t *testing.T) {
	p := harness.NewRuleProject(t)
	rule(p, "context", "artifact-landed",
		"on:\n  - event: PostFileWrite\n    match: event.path startsWith \"out/\"\nenter: ./enter.sh\nexit: ./exit.sh\n",
		map[string]string{
			"enter.sh": "#!/usr/bin/env bash\nset -uo pipefail\ninput=\"$(cat)\"\nsettled=\"$(printf '%s' \"$input\" | jq -r '.event.newContentKnown')\"\n[ \"$settled\" = true ] || exit 1\njq -n --arg p \"$(printf '%s' \"$input\" | jq -r '.event.path')\" '{artifact: $p}'\n",
			"exit.sh":  "#!/usr/bin/env bash\ncat >/dev/null\nexit 1\n",
		})
	kase(p, "context", "artifact-landed", "an-artifact-lands", "contexts:\n  artifact-landed: active\n", baseSetup,
		"- run: |\n    mkdir out\n    echo result > out/r.md\n    git add -A\n    git commit -q -m out\n- kind: PostFileWrite\n  path: out/r.md\n- kind: Stop\n")
	kase(p, "context", "artifact-landed", "another-path-does-not-wake-it", "contexts:\n  artifact-landed: inactive\n", baseSetup,
		"- run: |\n    echo result > elsewhere.md\n    git add -A\n    git commit -q -m elsewhere\n- kind: PostFileWrite\n  path: elsewhere.md\n- kind: Stop\n")

	res := p.Test("artifact-landed")
	require.Equal(t, 0, res.Code, res.Output)
	require.Contains(t, res.Output, "2 case(s) run, 0 failed")
}

// T001_27: a trajectory step the engine cannot understand fails the case rather than being
// ignored: an event kind that does not exist, a field its kind does not carry, a Post event
// given an expectation, a bash step that fails.
func TestT001_27_MalformedStepsFailTheCase(t *testing.T) {
	p := harness.NewRuleProject(t)
	rule(p, "gate", "g", "on:\n  - event: Stop\nchecks:\n  - script: ./ok.sh\n", map[string]string{"ok.sh": "#!/usr/bin/env bash\nexit 0\n"})
	kase(p, "gate", "g", "unknown-kind", "expect: permit\n", baseSetup, "- kind: PreFileMake\n  path: a\n")
	kase(p, "gate", "g", "typo-in-a-field", "expect: permit\n", baseSetup, "- kind: PreFileCreate\n  path: a\n  newContnet: x\n")
	kase(p, "gate", "g", "expect-on-a-post-event", "expect: permit\n", baseSetup,
		"- kind: PostTagWrite\n  tags: [a]\n  expect: permit\n- kind: Stop\n")
	kase(p, "gate", "g", "a-failing-run-step", "expect: permit\n", baseSetup, "- run: exit 4\n- kind: Stop\n")

	res := p.Test("g")
	require.Equal(t, 1, res.Code, res.Output)
	require.Contains(t, res.Output, "unknown event kind")
	require.Contains(t, res.Output, `no field "newContnet"`)
	require.Contains(t, res.Output, "delivered with the next Stop")
	require.Contains(t, res.Output, "run: exit 4) failed")
	require.Contains(t, res.Output, "4 case(s) run, 4 failed")
}
