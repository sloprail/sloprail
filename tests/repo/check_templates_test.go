package repo

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The two script-check templates the authoring skill ships teach opposite halves
// of the same discipline, so each is run against what it must decide: the
// file-guard template judges the committed files of a Changeset (always known, and
// refuses anything that is not a readable Changeset); the gate template judges
// pending (Pre*) bytes and fails closed on an unknown result.

func runTemplate(t *testing.T, name string, event map[string]any) (int, string) {
	t.Helper()
	return runTemplatePayload(t, name, map[string]any{"event": event})
}

func runTemplatePayload(t *testing.T, name string, body map[string]any) (int, string) {
	t.Helper()
	script := filepath.Join(repoRoot(t), "marketplace", "plugins", "sloprail", "skills", "authoring-guardrails", name)
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script)
	cmd.Stdin = strings.NewReader(string(payload))
	out, err := cmd.Output()
	if err == nil {
		return 0, string(out)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	}
	t.Fatal(err)
	return -1, ""
}

func TestFileGuardTemplateJudgesAChangesetAndFailsClosed(t *testing.T) {
	changeset := func(files ...map[string]any) map[string]any {
		return map[string]any{
			"event":     map[string]any{"kind": "Changeset"},
			"changeset": map[string]any{"files": files},
		}
	}
	file := func(status, content string) map[string]any {
		return map[string]any{"path": "a.md", "status": status, "newContent": content}
	}
	if code, out := runTemplatePayload(t, "check-template.sh", changeset(file("M", "fine"))); code != 0 {
		t.Errorf("a fine committed file was refused (%d): %s", code, out)
	}
	if code, out := runTemplatePayload(t, "check-template.sh", changeset(file("M", "fine"), file("A", "CHANGE-ME"))); code == 0 || !strings.Contains(out, "a.md") {
		t.Errorf("a committed file failing fine() was permitted, or the refusal did not name it (%d): %s", code, out)
	}
	// A deleted file has nothing to judge here.
	if code, out := runTemplatePayload(t, "check-template.sh", changeset(file("D", ""))); code != 0 {
		t.Errorf("a deleted file was refused (%d): %s", code, out)
	}
	// Anything that is not a readable Changeset is a refusal, never a pass: a file-guard
	// is never handed a file event, and an empty payload must not read as "no files".
	for name, body := range map[string]map[string]any{
		"a Pre kind":       {"event": map[string]any{"kind": "PreFileUpdate", "path": "a.md", "resultKnown": false, "newContent": ""}},
		"a Post kind":      {"event": map[string]any{"kind": "PostFileUpdate", "path": "a.md", "newContent": "x", "newContentKnown": true}},
		"no changeset":     {"event": map[string]any{"kind": "Changeset"}},
		"an empty payload": {},
		"files not a list": {"event": map[string]any{"kind": "Changeset"}, "changeset": map[string]any{"files": "x"}},
	} {
		if code, _ := runTemplatePayload(t, "check-template.sh", body); code == 0 {
			t.Errorf("the file-guard template permitted %s", name)
		}
	}
}

func TestGateTemplateRefusesAnUnknownResult(t *testing.T) {
	pre := func(known bool, content string) map[string]any {
		return map[string]any{"kind": "PreFileUpdate", "path": "a.md", "newContent": content, "resultKnown": known}
	}
	if code, out := runTemplate(t, "gate-check-template.sh", pre(true, "fine")); code != 0 {
		t.Errorf("a fine pending write was refused (%d): %s", code, out)
	}
	if code, _ := runTemplate(t, "gate-check-template.sh", pre(true, "CHANGE-ME")); code == 0 {
		t.Errorf("a pending write failing fine() was permitted")
	}
	code, out := runTemplate(t, "gate-check-template.sh", pre(false, ""))
	if code == 0 {
		t.Errorf("a write whose result could not be computed was permitted by the gate template")
	}
	if !strings.Contains(out, "could not be computed") {
		t.Errorf("the refusal does not say why: %s", out)
	}
	ev := map[string]any{"kind": "PostFileUpdate", "path": "a.md", "newContent": "x", "newContentKnown": true}
	if code, _ := runTemplate(t, "gate-check-template.sh", ev); code == 0 {
		t.Errorf("the gate template permitted a Post kind it is never handed")
	}
}
