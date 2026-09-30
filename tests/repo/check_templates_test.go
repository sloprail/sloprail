package repo

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The two script-check templates the authoring skill ships teach opposite halves
// of the same discipline, so each is run against the events it must decide:
// the file-guard template judges settled (Post*) bytes and fails closed on an
// unread file; the gate template judges pending (Pre*) bytes and fails closed on
// an unknown result. A file-guard never receives a Pre* kind, so the file-guard
// template refuses one rather than permit on `resultKnown` false.

func runTemplate(t *testing.T, name string, event map[string]any) (int, string) {
	t.Helper()
	script := filepath.Join(repoRoot(t), "marketplace", "plugins", "sloprail", "skills", "authoring-guardrails", name)
	payload, err := json.Marshal(map[string]any{"event": event})
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

func TestFileGuardTemplateIsPostOnlyAndFailsClosed(t *testing.T) {
	post := func(kind string, known bool, content string) map[string]any {
		return map[string]any{"kind": kind, "path": "a.md", "newContent": content, "newContentKnown": known}
	}
	if code, out := runTemplate(t, "check-template.sh", post("PostFileUpdate", true, "fine")); code != 0 {
		t.Errorf("a fine settled file was refused (%d): %s", code, out)
	}
	if code, _ := runTemplate(t, "check-template.sh", post("PostFileUpdate", true, "CHANGE-ME")); code == 0 {
		t.Errorf("a settled file failing fine() was permitted")
	}
	if code, _ := runTemplate(t, "check-template.sh", post("PostFileCreate", false, "")); code == 0 {
		t.Errorf("a settled file the engine could not read (newContentKnown false) was permitted")
	}
	// A file-guard is never handed a Pre kind: the template does not teach
	// "permit and let Stop judge it", it refuses the unexpected kind.
	for _, kind := range []string{"PreFileCreate", "PreFileUpdate"} {
		ev := map[string]any{"kind": kind, "path": "a.md", "resultKnown": false, "newContent": ""}
		if code, _ := runTemplate(t, "check-template.sh", ev); code == 0 {
			t.Errorf("the file-guard template permitted a %s", kind)
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
