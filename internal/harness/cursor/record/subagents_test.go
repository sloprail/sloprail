package record

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// conversationFile writes agent-transcripts/<id>/<id>.jsonl with a first user message and
// the prompts of its Task calls, and returns its path.
func conversationFile(t *testing.T, dir, id, prompt string, tasks ...string) string {
	t.Helper()
	p := filepath.Join(dir, "agent-transcripts", id, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"role":"user","message":{"content":[{"type":"text","text":"<timestamp/>\n<user_query>\n` + prompt + `\n</user_query>"}]}}` + "\n"
	for _, task := range tasks {
		body += `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Task","input":{"prompt":"` + task + `"}}]}}` + "\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func markRoot(t *testing.T, id string) {
	t.Helper()
	if err := AppendLine(id, StoredLine{Kind: KindRoot}); err != nil {
		t.Fatal(err)
	}
}

func TestSubagentLinkIsTheDispatchPrompt(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := t.TempDir()
	root := conversationFile(t, dir, "root-1", "do the work", "measure it")
	mid := conversationFile(t, dir, "mid-1", "measure it", "leaf work")
	leaf := conversationFile(t, dir, "leaf-1", "leaf work")
	stranger := conversationFile(t, dir, "other-1", "something else")
	markRoot(t, "root-1")
	markRoot(t, "other-1")

	tr := Transcripts{}
	for path, want := range map[string]string{mid: root, leaf: mid, root: "", stranger: ""} {
		if got := tr.ParentOf(path); got != want {
			t.Errorf("ParentOf(%s) = %q, want %q", filepath.Base(path), got, want)
		}
	}
	var got []string
	for _, f := range tr.SubagentFiles(root) {
		got = append(got, f.Path)
	}
	if want := []string{leaf, mid}; !reflect.DeepEqual(got, want) {
		t.Errorf("SubagentFiles(root) = %v, want %v", got, want)
	}
}

func TestSubagentWithTwoDispatchersHasNoParent(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := t.TempDir()
	a := conversationFile(t, dir, "root-a", "go", "same words")
	conversationFile(t, dir, "root-b", "go", "same words")
	sub := conversationFile(t, dir, "sub-1", "same words")
	markRoot(t, "root-a")
	markRoot(t, "root-b")

	if got := (Transcripts{}).ParentOf(sub); got != "" {
		t.Errorf("a prompt two sessions dispatched named the parent %q; it must name none", got)
	}
	if got := (Transcripts{}).SubagentFiles(a); len(got) != 0 {
		t.Errorf("an ambiguous sub-agent was attributed to a root: %v", got)
	}
}

func TestARootIsNeverASubagentOfItsOwnPrompt(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := t.TempDir()
	// A session whose own prompt equals a Task prompt of another is still a root.
	conversationFile(t, dir, "root-a", "go", "words")
	b := conversationFile(t, dir, "root-b", "words")
	markRoot(t, "root-b")
	if got := (Transcripts{}).ParentOf(b); got != "" {
		t.Errorf("a root was given the parent %q", got)
	}
}
