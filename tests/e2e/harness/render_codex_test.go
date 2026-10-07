package harness

import (
	"errors"
	"strings"
	"testing"
)

func TestCodexRendersWhatCodexCanDo(t *testing.T) {
	script, err := codexDriver{}.RenderScript(Turns("fin",
		Write("w1", "/p/a.txt", "one\ntwo\n"),
		Edit("e1", "/p/a.txt", "one", "uno"),
		Bash("b1", "echo hi"),
		Say("s1", "hello"),
		Dispatch("d1", "go", "/tmp/sub.sh", ""),
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, frag := range []string{
		`*** Add File: /p/a.txt\n+one\n+two\n*** End Patch`,
		`*** Update File: /p/a.txt\n@@\n-one\n+uno\n*** End Patch`,
		`"id":"w1-slop-turn-0-w1","input":`,
		`"name":"Bash"`,
		`"name":"spawn_agent"`,
		`"name":"wait_agent"`,
		`{"text":"fin","type":"text"}`,
	} {
		if !strings.Contains(script, frag) {
			t.Errorf("script lacks %s:\n%s", frag, script)
		}
	}
}

func TestCodexReportsWhatItCannotDo(t *testing.T) {
	cases := map[string]Turn{
		"Skill":           Skill("k1", "x"),
		"ToolUse":         ToolUse("t1", "fill_form", map[string]string{"a": "b"}),
		"ToolUseJSON":     ToolUseJSON("t2", "x", `{}`),
		"BashBatch":       BashBatch("bb", "a", "b"),
		"BackgroundAgent": Background("bg", "Agent", map[string]string{"prompt": "x"}),
		"ToolResult":      ToolResult("r1", "x"),
		"IsolatedDispat":  Dispatch("d1", "go", "/tmp/s.sh", "worktree"),
	}
	for name, turn := range cases {
		_, err := codexDriver{}.RenderScript(Turns("fin", turn))
		var u *UnsupportedError
		if !errors.As(err, &u) {
			t.Errorf("%s: want an UnsupportedError, got %v", name, err)
		}
	}
}
