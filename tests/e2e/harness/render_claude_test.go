package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// canonicalJSON is a line with its object keys in sorted order (a tool's input
// was written in map order before, and is sorted now: the content is what must not move).
func canonicalJSON(line string) string {
	var v any
	if err := json.Unmarshal([]byte(line), &v); err != nil {
		return "NOTJSON:" + line
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// TestClaudeRenderingUnchanged: every Turn constructor still renders, through
// the Claude driver, the record it rendered before Turns were abstract actions.
// testdata/claude_render.golden holds that record as the constructors produced it
// then (one line per case: name, record, whether it names a launched output, a
// launched task).
func TestClaudeRenderingUnchanged(t *testing.T) {
	want, err := os.ReadFile("testdata/claude_render.golden")
	if err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	for _, c := range renderCases() {
		line, err := renderClaude(c.Turn.act)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		fmt.Fprintf(&got, "%s\t%s\t%v\t%v\n", c.Name, canonicalJSON(line), c.Turn.launchedOutput, c.Turn.launchedTask)
	}
	if got.String() != string(want) {
		gl, wl := strings.Split(got.String(), "\n"), strings.Split(string(want), "\n")
		for i := range wl {
			if i >= len(gl) || gl[i] != wl[i] {
				t.Fatalf("rendering changed at case %d:\n got: %s\nwant: %s", i, gl[min(i, len(gl)-1)], wl[i])
			}
		}
		t.Fatalf("rendering changed: %d cases, golden has %d", len(gl), len(wl))
	}
}

// TestClaudeScriptWrapsMarkers: the script around a turn is the one it was: the turn fires
// once, gated on a marker made of its own id.
func TestClaudeScriptWrapsMarkers(t *testing.T) {
	script, err := claudeDriver{}.RenderScript(Turns("fin", Write("w1", "/p/a", "x"), Say("s1", "hi")))
	if err != nil {
		t.Fatal(err)
	}
	for _, frag := range []string{`grep -q "slop-turn-0-w1"`, `"id":"w1-slop-turn-0-w1"`, `{"type":"result","subtype":"success","result":"fin","is_error":false}`} {
		if !strings.Contains(script, frag) {
			t.Errorf("script lacks %s:\n%s", frag, script)
		}
	}
}
