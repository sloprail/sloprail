package e2e

import (
	"strings"
	"testing"
)

// PreToolUse is the event a rule binds to when neither a file nor a command
// event fits the tool it wants to gate. This suite proves the whole path: the
// agent reaches for a tool, the engine emits a semantic PreToolUse carrying that
// tool's name and input, a matcher narrows on the tool, and a bound hook runs.
//
// # Why these are end-to-end and not unit tests
//
// The unit tests in internal/tooluse prove the module builds a PreToolUse from a
// pending call. They cannot prove the engine ASKS it to at the pre-tool hook, nor
// that a guardrail may bind `PreToolUse` and have its hook reached. That whole
// wiring — module registered, kind bindable, event dispatched, matcher compiled
// against its declaration — is only observable from outside the binary, which is
// what this drives.
//
// # Why the Skill tool
//
// The tool has to be one the file and command derivation do NOT already answer,
// or the test would prove nothing about PreToolUse specifically — a Bash command
// produces a PreCommandInvoke and a Write produces a PreFileCreate. The mock's
// Skill tool carries neither a file path nor a command line, so nothing but
// PreToolUse can see it. A rule bound to PreToolUse and narrowed to `tool ==
// "Skill"` therefore fires on a tool call that no other event describes.

// recordTool writes the tool and input it was shown into a ledger, so the test
// can read off the wire exactly what PreToolUse carried.
const recordTool = `#!/bin/sh
cat >> events.jsonl
printf '\n' >> events.jsonl
`

// boundToPreToolUse records every PreToolUse whose tool is Skill.
const boundToPreToolUse = `---
hooks:
  PreToolUse:
    - matcher: tool == "Skill"
      hooks:
        - type: command
          command: ./record.sh
---

# Records a PreToolUse for the Skill tool
`

// T029_01: a rule bound to PreToolUse and narrowed by tool fires on a tool call
// that no file or command event covers.
func TestT029_01_PreToolUseBindsToATool(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "watch-skill", boundToPreToolUse, map[string]string{"record.sh": recordTool})

	e.Run(proj, "s-029-01", "load a skill", Turns("done",
		Skill("s1", "some-skill"),
	))

	events := strings.Join(e.Ledger(proj, "watch-skill", "events.jsonl"), "\n")
	if events == "" {
		t.Fatalf("no PreToolUse reached the hook — a rule bound to PreToolUse did not fire on the Skill tool")
	}
	// The kind is PreToolUse, and it names the tool the scenario invoked.
	if !strings.Contains(events, `"kind":"PreToolUse"`) {
		t.Errorf("the event a rule bound to PreToolUse received was not a PreToolUse:\n%s", events)
	}
	if !strings.Contains(events, `"tool":"Skill"`) {
		t.Errorf("PreToolUse did not carry the tool name the matcher narrowed on:\n%s", events)
	}
	// The input the harness reported rides along, so a rule that wants more than
	// the tool name can reach it.
	if !strings.Contains(events, "some-skill") {
		t.Errorf("PreToolUse did not carry the tool's input:\n%s", events)
	}
}

// T029_02: the same rule does NOT fire on a different tool.
//
// The control for the matcher. Without it, T029_01 would pass against an engine
// that fired PreToolUse on every tool and ignored the `tool ==` narrowing — the
// event bindable but the matcher inert.
func TestT029_02_TheMatcherNarrowsByTool(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "watch-skill", boundToPreToolUse, map[string]string{"record.sh": recordTool})

	// A Bash turn — a tool the rule does not name. `true` touches nothing, so no
	// file changes and the only reason the hook could run is a PreToolUse the
	// matcher failed to narrow.
	e.Run(proj, "s-029-02", "run a command", Turns("done",
		Bash("b1", "true"),
	))

	events := strings.Join(e.Ledger(proj, "watch-skill", "events.jsonl"), "\n")
	if strings.Contains(events, "Bash") {
		t.Errorf("a rule narrowed to `tool == \"Skill\"` fired on a Bash tool call — the matcher did not narrow:\n%s", events)
	}
}
