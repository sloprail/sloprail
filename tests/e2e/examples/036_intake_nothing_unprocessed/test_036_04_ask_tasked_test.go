package e2e

// The multi-ask-turn eval scores that the user's GENUINE ask is tasked
// (examples/intake-nothing-unprocessed/eval/ask-tasked.sh): a tasks/*.md file
// references the prompt's own transcript line. The gate is satisfied just as well
// by #skip-ping that line, and counts tool results as "user messages" too, so the
// scorer has to find the prompt among them and see a task, not a skip. Refuse
// first (skipped or untasked), then pass (tasked).

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestT036_07_AskTaskedRefusesASkipThenPassesATask(t *testing.T) {
	root := repoRoot(t)
	script := filepath.Join(root, "examples", "intake-nothing-unprocessed", "eval", "ask-tasked.sh")
	prompt := filepath.Join(root, "examples", "intake-nothing-unprocessed", "eval", "multi-ask-turn", "prompt.md")
	promptText, err := os.ReadFile(prompt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not installed")
	}

	entry := func(line int, content any) map[string]any {
		return map[string]any{"type": "user", "line": line, "message": map[string]any{"content": content}}
	}
	toolResult := entry(24, []any{map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "ok"}})
	run := func(t *testing.T, project, transcript string, entries ...map[string]any) (line any, tasked, skipped bool) {
		t.Helper()
		in, _ := json.Marshal(entries)
		cmd := exec.Command("sh", script, transcript, prompt, project)
		cmd.Stdin = strings.NewReader(string(in))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("ask-tasked.sh: %v\n%s", err, out)
		}
		var got struct {
			Line    any  `json:"line"`
			Tasked  bool `json:"tasked"`
			Skipped bool `json:"skipped"`
		}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("not JSON: %q", out)
		}
		return got.Line, got.Tasked, got.Skipped
	}

	project := t.TempDir()
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"text":"#skip 4\n#skip 24"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	text := strings.TrimSpace(string(promptText))
	cut := strings.Index(text, " ")

	// Refuse: the ask exists and was only #skip-ped; and a task that references
	// some other line (a tool result) is no task for the ask.
	line, tasked, skipped := run(t, project, transcript, entry(4, text), toolResult)
	if line != float64(4) || tasked || !skipped {
		t.Fatalf("a skipped ask: line=%v tasked=%v skipped=%v, want 4 false true", line, tasked, skipped)
	}
	tasks := filepath.Join(project, "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasks, "other.md"), []byte("[x](/p/session.jsonl:24-24)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, tasked, _ := run(t, project, transcript, entry(4, text), toolResult); tasked {
		t.Fatal("a task referencing a tool result's line must not count as tasking the ask")
	}
	// Refuse: no entry is the prompt.
	if line, tasked, _ := run(t, project, transcript, toolResult); line != nil || tasked {
		t.Fatalf("no prompt in the record: line=%v tasked=%v, want null false", line, tasked)
	}

	// Pass: a task references the ask's own line — however the entry spells it.
	if err := os.WriteFile(filepath.Join(tasks, "fix.md"), []byte("[the ask](/p/session.jsonl:4-4)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]any{
		"a string":                       text,
		"leading whitespace and newline": "\n  \t" + text,
		"line breaks as in the file":     strings.ReplaceAll(text, " ", "\n"),
		"an array of text blocks":        []any{map[string]any{"type": "text", "text": text}},
		"an array, split in two":         []any{map[string]any{"type": "text", "text": text[:cut]}, map[string]any{"type": "text", "text": text[cut:]}},
	} {
		t.Run(name, func(t *testing.T) {
			line, tasked, _ := run(t, project, transcript, toolResult, entry(4, content))
			if line != float64(4) || !tasked {
				t.Fatalf("line=%v tasked=%v, want 4 true", line, tasked)
			}
		})
	}
}
