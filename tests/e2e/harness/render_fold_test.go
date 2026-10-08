package harness

import (
	"strings"
	"testing"
)

func TestCodexRendersACallWithOutputAsTheCommandPrintingIt(t *testing.T) {
	call, out := CallWithOutput("r1", "Bash", map[string]string{"command": "true"}, "ok it's green\n")
	script, err := codexDriver{}.RenderScript(Turns("fin", call, out))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, `printf`) || strings.Count(script, `"name":"Bash"`) != 1 {
		t.Errorf("the pair is not one command printing the output:\n%s", script)
	}
	if strings.Contains(script, "it's green") || strings.Contains(script, `green`) {
		t.Errorf("the output text is literally in the command:\n%s", script)
	}
}

func TestCursorRendersACallWithOutputAsTheCommandPrintingIt(t *testing.T) {
	call, out := CallWithOutput("r1", "Bash", map[string]string{"command": "true"}, "ok it's green\n")
	script, err := cursorDriver{}.RenderScript(Turns("fin", Bash("b0", "echo hi"), call, out))
	if err != nil {
		t.Fatal(err)
	}
	// two steps (progress 0 and 1): the pair is one, so no step is numbered 2
	if !strings.Contains(script, `printf`) || strings.Count(script, `"name":"Bash"`) != 2 || strings.Contains(script, `-eq 2 ]`) {
		t.Errorf("the pair is not one step printing the output:\n%s", script)
	}
	if strings.Contains(script, `green`) {
		t.Errorf("the output text is literally in the step's command:\n%s", script)
	}
}
