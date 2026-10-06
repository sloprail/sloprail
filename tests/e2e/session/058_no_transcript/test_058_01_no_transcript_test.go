package e2e

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// A session with no transcript (#274): codex --ephemeral sends transcript_path null on every
// hook; claude --no-session-persistence sends the path the session WOULD have written, a file that
// never exists. The agent cannot fix that, so refusing would deadlock the run: sloprail switches
// itself off for the session, says so loudly (a systemMessage the harness shows, once, and stderr),
// and no rule fires. The hooks are driven with exactly those payloads (the pinned claude mock
// predates --no-session-persistence).

const refuseWrites = "on:\n  - event: PreFileWrite\n    match: event.path startsWith \"memories/\"\nchecks:\n  - script: ./check.sh\n"

const refuseAlways = "#!/bin/sh\ncat >/dev/null\necho '{\"reason\":\"RULE-FIRED\"}'\nexit 1\n"

func preToolPayload(t *testing.T, proj, session string, transcript any) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"session_id": session, "transcript_path": transcript, "cwd": proj, "hook_event_name": "PreToolUse",
		"tool_name": "Write", "tool_use_id": "t1",
		"tool_input": map[string]any{"file_path": "memories/note.md", "content": "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func notice(t *testing.T, out string) bool {
	t.Helper()
	return strings.Contains(out, "guardrails are OFF for this session")
}

// T058_01: the gate refuses when a transcript exists (the control), and does nothing, with the
// notice, when the path is missing (claude --no-session-persistence) or null (codex --ephemeral).
func TestT058_01_NoTranscriptSwitchesTheSessionOff(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "refuse-writes", refuseWrites, map[string]string{"check.sh": refuseAlways})
	e.Run(proj, "s-058-01-real", "hello", harness.Turns("done", harness.Bash("b1", "true")))

	// Control: a session whose transcript exists is refused by the rule.
	real := e.CLIDirectStdinEnv(proj, preToolPayload(t, proj, "s-058-01-real", e.TranscriptPath(proj, "s-058-01-real")),
		e.SessionEnv("s-058-01-real"), "sr-session", "pre-tool")
	if !strings.Contains(real.Output, "RULE-FIRED") || notice(t, real.Output) {
		t.Fatalf("control: the rule did not fire in a session with a transcript:\n%s", real.Output)
	}

	missing := filepath.Join(t.TempDir(), "never-written.jsonl")
	for name, transcript := range map[string]any{"missing file": missing, "null path": nil} {
		session := "s-058-01-" + strings.ReplaceAll(name, " ", "-")
		res := e.CLIDirectStdinEnv(proj, preToolPayload(t, proj, session, transcript), e.SessionEnv(session), "sr-session", "pre-tool")
		if res.Code != 0 || strings.Contains(res.Output, "RULE-FIRED") || strings.Contains(res.Output, "deny") {
			t.Errorf("%s: a rule fired (or the hook failed) in a session with no transcript:\n%s", name, res.Output)
		}
		if !notice(t, res.Output) || !strings.Contains(res.Output, `"systemMessage"`) {
			t.Errorf("%s: no visible notice that guardrails are off:\n%s", name, res.Output)
		}
		// Once on the screen: the second hook of the session adds no second systemMessage but
		// still does nothing.
		again := e.CLIDirectStdinEnv(proj, preToolPayload(t, proj, session, transcript), e.SessionEnv(session), "sr-session", "pre-tool")
		if strings.Contains(again.Output, `"systemMessage"`) || strings.Contains(again.Output, "RULE-FIRED") {
			t.Errorf("%s: the second hook repeated the message or fired a rule:\n%s", name, again.Output)
		}
	}
}

// T058_02: the Stop and SessionStart hooks of such a session do nothing either (at SessionStart a missing
// file proves nothing: the file of a fresh session is not written yet, so it never fails there).
func TestT058_02_StopAndStartDoNothingWithoutATranscript(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	missing := filepath.Join(t.TempDir(), "never-written.jsonl")

	stop, _ := json.Marshal(map[string]any{"session_id": "s-058-02", "transcript_path": missing, "cwd": proj, "stop_hook_active": false, "hook_event_name": "Stop"})
	res := e.CLIDirectStdinEnv(proj, string(stop), e.SessionEnv(""), "sr-session", "stop")
	if res.Code != 0 || !notice(t, res.Output) || strings.Contains(res.Output, `"decision":"block"`) {
		t.Errorf("Stop in a session with no transcript must allow the stop, with the notice:\n%s", res.Output)
	}

	start, _ := json.Marshal(map[string]any{"session_id": "s-058-02b", "transcript_path": nil, "cwd": proj, "source": "startup", "hook_event_name": "SessionStart"})
	res = e.CLIDirectStdinEnv(proj, string(start), e.SessionEnv(""), "sr-session", "start")
	if res.Code != 0 {
		t.Errorf("SessionStart with a null transcript path must not fail (the file may simply not be written yet; the first later hook tells):\n%s", res.Output)
	}
}
