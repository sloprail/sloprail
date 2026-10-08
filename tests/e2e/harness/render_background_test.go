package harness

import (
	"strings"
	"testing"
)

// A background command on Codex and Cursor is the command with its output sent to a file a later
// read reads (driver_background.go).
func TestCodexAndCursorRenderABackgroundCommandAsAnOutputFile(t *testing.T) {
	turns := Turns("fin",
		Background("bg1", "Bash", map[string]string{"command": "echo hi"}),
		ReadLaunchedOutput("r1"),
	)
	codex, err := codexDriver{}.RenderScript(turns)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := cursorDriver{}.RenderScript(turns)
	if err != nil {
		t.Fatal(err)
	}
	launch := `( echo hi\n) > \"${TMPDIR:-/tmp}/sr-bg-bg1.output\" 2>&1`
	for name, c := range map[string]struct {
		script string
		frags  []string
	}{
		"codex":  {codex, []string{launch, `cat \"${TMPDIR:-/tmp}/sr-bg-bg1.output\"`}},
		"cursor": {cursor, []string{launch, `"file_path":"@@TMPDIR@@/sr-bg-bg1.output"`, `sed "s|@@TMPDIR@@|${TMPDIR:-/tmp}|g"`}},
	} {
		for _, frag := range c.frags {
			if !strings.Contains(c.script, frag) {
				t.Errorf("%s: script lacks %s:\n%s", name, frag, c.script)
			}
		}
	}
}
