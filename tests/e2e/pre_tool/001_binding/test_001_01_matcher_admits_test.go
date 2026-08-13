package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// The declaration under test: one guardrail, bound to a file about to be
// created, narrowed to a directory. Its hook refuses everything it is given —
// so what the test proves is which events reach it, not what it decides.
const refuseUnderGuarded = `---
hooks:
  PreFileCreate:
    - matcher: path startsWith "guarded/"
      hooks:
        - type: command
          command: ./refuse.sh
---

# Refuses any write it is shown

The hook is unconditional on purpose: a test about binding should fail when the
wrong event arrives, not when the right event is judged differently.
`

const refuseScript = `#!/bin/sh
cat >/dev/null
echo '{"decision":"block","reason":"this path is guarded"}'
exit 1
`

// T001_01: a write the matcher admits is refused.
//
// The positive half of hook_within_binding: the hook runs where its binding
// says it should.
func TestT001_01_MatcherAdmitsWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "guarded-dir", refuseUnderGuarded, map[string]string{"refuse.sh": refuseScript})

	out, code := e.Hook(proj, []string{"session", "pre-tool"}, map[string]any{
		"cwd":        proj,
		"tool_name":  "Write",
		"tool_input": map[string]any{"file_path": "guarded/notes.md", "content": "hello"},
	})

	if code != 0 {
		t.Fatalf("engine exited %d, want 0 — a refusal is written, not exited: %s", code, out)
	}
	if !strings.Contains(out, "this path is guarded") {
		t.Fatalf("want the hook's refusal in the output, got: %s", out)
	}

	var got struct {
		HookSpecificOutput struct {
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not the shape this harness expects: %v\n%s", err, out)
	}
	if got.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("want deny, got %q", got.HookSpecificOutput.PermissionDecision)
	}
}

// T001_02: a write the matcher does not admit is permitted.
//
// The negative half, and the one that would catch a matcher that admitted
// everything — a guardrail that refuses every write looks identical to a
// correct one until something outside its scope is tried.
func TestT001_02_MatcherRejectsOtherPath(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "guarded-dir", refuseUnderGuarded, map[string]string{"refuse.sh": refuseScript})

	out, code := e.Hook(proj, []string{"session", "pre-tool"}, map[string]any{
		"cwd":        proj,
		"tool_name":  "Write",
		"tool_input": map[string]any{"file_path": "elsewhere/notes.md", "content": "hello"},
	})

	if code != 0 {
		t.Fatalf("engine exited %d, want 0: %s", code, out)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("want silence for a path outside the binding, got: %s", out)
	}
}

// T001_03: a project that declares nothing is left alone.
//
// The engine has no opinion of its own. Every refusal traces to something a
// project declared, and a project that declared nothing should see no
// difference from not having installed this at all.
func TestT001_03_NoDeclarationsPermits(t *testing.T) {
	e := New(t)
	proj := e.Project()

	out, code := e.Hook(proj, []string{"session", "pre-tool"}, map[string]any{
		"cwd":        proj,
		"tool_name":  "Write",
		"tool_input": map[string]any{"file_path": "guarded/notes.md", "content": "hello"},
	})

	if code != 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("want silence with no guardrails declared, got %d: %s", code, out)
	}
}
