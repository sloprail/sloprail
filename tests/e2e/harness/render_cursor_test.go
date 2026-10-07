package harness

import (
	"errors"
	"strings"
	"testing"
)

func TestCursorRendersWhatCursorCanDo(t *testing.T) {
	script, err := cursorDriver{}.RenderScript(Turns("fin",
		Write("w1", "/p/a.txt", "one\n"),
		Bash("b1", "echo hi"),
		Compact("k1"),
		Dispatch("d1", "go", "/tmp/sub.sh", ""),
		Skill("k2", "x"),
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, frag := range []string{
		`"name":"Write"`,
		`"name":"Bash"`,
		`{"type":"compact","trigger":"manual"}`,
		`"name":"Task"`,
		`"name":"Read"`, // a skill is loaded by reading its SKILL.md: Cursor has no skill tool
		`/.claude/skills/x/SKILL.md`,
		// the final reply is an assistant line: the run's result is what the assistant said
		`{"message":{"content":[{"text":"fin","type":"text"}]},"type":"assistant"}`,
	} {
		if !strings.Contains(script, frag) {
			t.Errorf("script lacks %s:\n%s", frag, script)
		}
	}
}

func TestCursorReportsWhatItCannotDo(t *testing.T) {
	cases := map[string]Turn{
		"BashBatch":      BashBatch("bb", "a", "b"),
		"Background":     Background("bg", "Bash", map[string]string{"command": "x"}),
		"ToolResult":     ToolResult("r1", "x"),
		"IsolatedDispat": Dispatch("d1", "go", "/tmp/s.sh", "worktree"),
	}
	for name, turn := range cases {
		_, err := cursorDriver{}.RenderScript(Turns("fin", turn))
		var u *UnsupportedError
		if !errors.As(err, &u) {
			t.Errorf("%s: want an UnsupportedError, got %v", name, err)
		}
	}
}

func TestCodexRendersACompactionAsAControlRecord(t *testing.T) {
	script, err := codexDriver{}.RenderScript(Turns("fin", Bash("b1", "echo hi"), Compact("k1")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, `{"type":"compact","trigger":"manual"}`) {
		t.Errorf("no compact record in:\n%s", script)
	}
}
