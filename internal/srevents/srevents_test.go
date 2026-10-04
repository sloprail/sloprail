package srevents

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestEmitAppendsOneLinePerEvent(t *testing.T) {
	f := filepath.Join(t.TempDir(), "ev.jsonl")
	t.Setenv(EnvFile, f)
	Emit(Event{Kind: GateChecked, Rule: "sloprail/x", Outcome: Refused, On: "PreCommandInvoke", ToolUseID: "t1", Reason: "no"})
	Emit(Event{Kind: ContextActivated, Rule: "c"})
	b, _ := os.ReadFile(f)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d", len(lines))
	}
	var e Event
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil || e.Rule != "sloprail/x" || e.ToolUseID != "t1" || e.FiredAt == "" {
		t.Fatalf("bad line %q (%v)", lines[0], err)
	}
}

func TestEmitUnsetWritesNothing(t *testing.T) {
	t.Setenv(EnvFile, "")
	var w bytes.Buffer
	EmitTo(&w, Event{Kind: GateChecked})
	if w.Len() != 0 {
		t.Fatal(w.String())
	}
}

func TestEmitFailureWarnsOnly(t *testing.T) {
	t.Setenv(EnvFile, filepath.Join(t.TempDir(), "missing-dir", "ev.jsonl"))
	var w bytes.Buffer
	EmitTo(&w, Event{Kind: GateChecked})
	if !strings.Contains(w.String(), "could not write") {
		t.Fatalf("no warning: %q", w.String())
	}
}

func TestEmitConcurrent(t *testing.T) {
	f := filepath.Join(t.TempDir(), "ev.jsonl")
	t.Setenv(EnvFile, f)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); Emit(Event{Kind: GateChecked, Rule: "r", Outcome: Permitted}) }()
	}
	wg.Wait()
	b, _ := os.ReadFile(f)
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var e Event
		if json.Unmarshal([]byte(l), &e) != nil {
			t.Fatalf("torn line %q", l)
		}
	}
}

func TestRule(t *testing.T) {
	if Rule("p", "n") != "p/n" || Rule("", "n") != "n" {
		t.Fatal()
	}
}
