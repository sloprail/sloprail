package agent

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/harnessmock"
)

func TestReadEvents(t *testing.T) {
	p := filepath.Join(t.TempDir(), "e.jsonl")
	if ev, raw, err := readEvents(p); err != nil || len(ev) != 0 || raw != nil {
		t.Fatalf("missing file: %v %v %v", ev, raw, err)
	}
	os.WriteFile(p, []byte("{\"kind\":\"a\"}\n\n{\"kind\":\"b\"}\n"), 0o644)
	ev, raw, err := readEvents(p)
	if err != nil || len(ev) != 2 || len(raw) != 2 {
		t.Fatalf("%v %v %v", ev, raw, err)
	}
	os.WriteFile(p, []byte("nope\n"), 0o644)
	if _, _, err := readEvents(p); err == nil {
		t.Fatal("bad JSON accepted")
	}
}

func TestWriteProjectSettingsExcludesFromGit(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git: %v %s", err, out)
	}
	for i := 0; i < 2; i++ {
		if err := writeProjectSettings(dir, []byte("{}")); err != nil {
			t.Fatal(err)
		}
	}
	out, _ := exec.Command("git", "-C", dir, "status", "--porcelain", "-uall").Output()
	if strings.Contains(string(out), ".claude") {
		t.Fatalf("settings show in git status: %s", out)
	}
	ex, _ := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if strings.Count(string(ex), ".claude/settings.local.json") != 1 {
		t.Fatalf("exclude = %q", ex)
	}
}

func TestCheckoutRootFindsThisCheckout(t *testing.T) {
	t.Setenv("SR_TEST_CHECKOUT", "")
	r, err := checkoutRoot()
	if err != nil || !isCheckout(r) {
		t.Fatalf("%q %v", r, err)
	}
}

func TestRunWithoutMockIsAnError(t *testing.T) {
	t.Setenv("A10N_CLAUDE_MOCK", "")
	t.Setenv("PATH", t.TempDir())
	if _, err := Run(Options{Script: "x.sh"}); err == nil {
		t.Fatal("want an error")
	}
}

// Runs the real mock: the agent appends an event to the per-run events file,
// which must come back in the result and in the outer SR_EVENTS_FILE.
func TestRunRealMock(t *testing.T) {
	if _, err := harnessmock.Path(); err != nil {
		t.Skipf("pinned mock unavailable: %v", err)
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git: %v %s", err, out)
	}
	script := filepath.Join(dir, "agent.sh")
	os.WriteFile(script, []byte(`#!/bin/sh
echo '{"kind":"GateChecked","rule":"r","outcome":"permitted"}' >> "$SR_EVENTS_FILE"
echo '{"type":"result","subtype":"success","result":"done"}'
`), 0o755)
	outer := filepath.Join(t.TempDir(), "outer.jsonl")
	t.Setenv("SR_EVENTS_FILE", outer)
	t.Chdir(dir)

	res, err := Run(Options{Script: script, Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Exit != 0 || len(res.Events) != 1 {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(res.Stream); err != nil {
		t.Fatal(err)
	}
	if res.Session == "" {
		t.Fatal("no session transcript path")
	}
	b, _ := os.ReadFile(outer)
	var ev map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &ev); err != nil || ev["rule"] != "r" {
		t.Fatalf("outer = %q %v", b, err)
	}
}
