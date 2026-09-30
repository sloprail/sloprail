package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// PreToolUse is the event a rule binds to when neither a file nor a command
// event fits the tool it wants to gate. This suite proves the whole path: the
// agent reaches for a tool, the engine emits a semantic PreToolUse carrying that
// tool's name and input, a matcher narrows on the tool, and a bound check runs.
//
// # Why these are end-to-end and not unit tests
//
// The unit tests in internal/tooluse prove the module builds a PreToolUse from a
// pending call. They cannot prove the engine ASKS it to at the pre-tool hook, nor
// that a gate may trigger on `PreToolUse` and have its check reached. That whole
// wiring — module registered, kind gate-eligible, event dispatched, trigger match
// compiled against its declaration — is only observable from outside the binary,
// which is what this drives.
//
// # Why the Skill tool
//
// The tool has to be one the file and command derivation do NOT already answer,
// or the test would prove nothing about PreToolUse specifically — a Bash command
// produces a PreCommandInvoke and a Write produces a PreFileCreate. The mock's
// Skill tool carries neither a file path nor a command line, so nothing but
// PreToolUse can see it. A gate triggering on PreToolUse and narrowed to
// `event.tool == "Skill"` therefore fires on a tool call that no other event
// describes.
//
// PreToolUse is the harness-native PRE-ACTION event, and the new dispatch's
// pre-action rule is a GATE: it triggers `on: [{event: PreToolUse}]`, exactly the
// vehicle the decision rule names for a pre-action block keyed to an event kind.
// The old declaration bound `hooks: PreToolUse:` with a `matcher: tool == "..."`;
// the gate names the same event under `on.event` and narrows with `on.match`,
// which reads the event nested under `event` (GateMatchScope), so the narrowing is
// `event.tool == "Skill"` rather than the flat `tool`. The check receives the FLAT
// GateCheckPayload (`.event.kind`, `.event.tool`, `.event.input`), which is what
// the observation parses back — never the OLD nested `.event.fields.*`.
//
// The check RECORDS what PreToolUse carried and PERMITS (exit 0), so which events
// reach it is the engine's answer rather than a verdict's. The ledger is read with
// e.GateLedgerLines (the gate analogue of e.FileGuardLedgerLines), from the gate's
// own folder under `.sloprail/gate/<name>/` where SR_GUARDRAIL_DIR points and the
// check's cwd sits.

// recordTool writes the flat GateCheckPayload it was shown into a ledger under the
// gate's own folder, then permits — so a test can read off the wire exactly what
// PreToolUse carried without the recording changing the outcome. $SR_GUARDRAIL_DIR
// is the gate's folder (`.sloprail/gate/<name>/`), the new-format ledger idiom.
const recordTool = `#!/bin/sh
cat >> "$SR_GUARDRAIL_DIR/events.jsonl"
printf '\n' >> "$SR_GUARDRAIL_DIR/events.jsonl"
exit 0
`

// boundToPreToolUse is a gate that records every PreToolUse whose tool is Skill.
// The trigger's match reads the event nested under `event` (a gate scope nests
// where a file scope is flat), so the tool narrowing is `event.tool`.
const boundToPreToolUse = `on:
  - event: PreToolUse
    match: event.tool == "Skill"
checks:
  - script: ./record.sh
`

// toolEventOf parses the flat GateCheckPayload the check recorded: `.event.kind`
// and `.event.tool` directly under `event`, NOT under an `event.fields` envelope
// the old format wrote. The input rides along under `.event.input`, an open map,
// read here as raw JSON so a test can assert a value appears within it.
func toolEventOf(t *testing.T, line string) (kind, tool, inputRaw string) {
	t.Helper()
	var p struct {
		Event struct {
			Kind  string          `json:"kind"`
			Tool  string          `json:"tool"`
			Input json.RawMessage `json:"input"`
		} `json:"event"`
	}
	if err := json.Unmarshal([]byte(line), &p); err != nil {
		t.Fatalf("the check was handed something that is not a gate payload: %v\n%s", err, line)
	}
	return p.Event.Kind, p.Event.Tool, string(p.Event.Input)
}

// T029_01: a gate triggering on PreToolUse and narrowed by tool fires on a tool
// call that no file or command event covers.
func TestT029_01_PreToolUseBindsToATool(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "watch-skill", boundToPreToolUse, map[string]string{"record.sh": recordTool})

	e.Run(proj, "s-029-01", "load a skill", Turns("done",
		Skill("s1", "some-skill"),
	))

	lines := e.GateLedgerLines(proj, "watch-skill", "events.jsonl")
	if len(lines) == 0 {
		t.Fatalf("no PreToolUse reached the check — a gate triggering on PreToolUse did not fire on the Skill tool")
	}
	// The kind is PreToolUse, and it names the tool the trigger narrowed on.
	kind, tool, inputRaw := toolEventOf(t, lines[0])
	if kind != "PreToolUse" {
		t.Errorf("the event a gate triggering on PreToolUse received was not a PreToolUse: %q", kind)
	}
	if tool != "Skill" {
		t.Errorf("PreToolUse did not carry the tool name the trigger narrowed on: %q", tool)
	}
	// The input the harness reported rides along, so a rule that wants more than
	// the tool name can reach it.
	if !strings.Contains(inputRaw, "some-skill") {
		t.Errorf("PreToolUse did not carry the tool's input: %q", inputRaw)
	}
}

// T029_02: the same gate does NOT fire on a different tool.
//
// The control for the trigger's match. Without it, T029_01 would pass against an
// engine that fired the gate on every tool and ignored the `event.tool ==`
// narrowing — the event gate-eligible but the match inert.
func TestT029_02_TheMatcherNarrowsByTool(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "watch-skill", boundToPreToolUse, map[string]string{"record.sh": recordTool})

	// A Bash turn — a tool the gate does not name. `true` touches nothing, so no
	// file changes and the only reason the check could run is a PreToolUse the
	// match failed to narrow.
	e.Run(proj, "s-029-02", "run a command", Turns("done",
		Bash("b1", "true"),
	))

	events := strings.Join(e.GateLedgerLines(proj, "watch-skill", "events.jsonl"), "\n")
	if strings.Contains(events, "Bash") {
		t.Errorf("a gate narrowed to `event.tool == \"Skill\"` fired on a Bash tool call — the match did not narrow:\n%s", events)
	}
}
