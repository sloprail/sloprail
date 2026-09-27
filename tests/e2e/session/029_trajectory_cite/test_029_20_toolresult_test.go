package e2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// trajectory tool-result — the LINE-oriented sibling of cite. Where cite turns a
// remembered QUOTE into a location, tool-result answers whether the entry at a cited
// LINE is a tool_result the session produced, and prints its content. A delivery
// OBSERVATION cites a transcript line as proof the work happened, and this is what
// confirms that line is a real tool-call result rather than the agent's prose.
//
//	the line is a tool_result       its content on stdout, exit 0
//	the line is not a tool_result   nothing on stdout, exit 1
//
// These drive the MOCK (never a hand-authored transcript): a tool_result lands as a
// user record carrying a tool_result block, which a10n-cli#470 taught the mock to
// forward + persist (the same capability the answer-envelope cases use), driven via
// the harness.ToolResult builder. The result's own line is read from the file the
// mock wrote (physicalLine), so the assertions rest on the real layout.

// toolResultCmd runs the compiled binary's tool-result against a path and line.
func toolResultCmd(e *Env, dir, path string, line int) harness.Result {
	return e.CLIDirect(dir, "sr-session", "trajectory", "tool-result",
		"--path", path, "--line", fmt.Sprintf("%d", line))
}

// TestT029_20_ToolResultLineExitsZeroWithContent: a line that IS a tool_result the
// session produced exits 0 and prints the result's content — the success case a
// delivery observation is grounded on.
func TestT029_20_ToolResultLineExitsZeroWithContent(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	// The agent ran the suite; the mock persists the result as a tool_result record
	// with distinctive content, after the seeded prompt.
	call, output := harness.CallWithOutput("r1", "Bash", map[string]string{"command": "true"}, "ok  sloprail/auth  0.42s\nPASS GREENMARKER")
	e.Run(proj, "s-029-16", "run the tests", Turns("done", call, output))
	path := e.TranscriptPath(proj, "s-029-16")

	resultLine := physicalLine(t, path, "GREENMARKER")
	if resultLine == 0 {
		t.Fatalf("the mock did not write the tool_result to the transcript\n%s", readFile(t, path))
	}

	res := toolResultCmd(e, proj, path, resultLine)
	if res.Code != 0 {
		t.Fatalf("a tool_result line exited %d, want 0:\n%s", res.Code, res.Output)
	}
	if !strings.Contains(res.Output, "GREENMARKER") {
		t.Fatalf("the result content was not printed:\n%s", res.Output)
	}
}

// TestT029_21_UserLineIsNotAToolResult: the line of the user's own PROMPT is not a
// tool_result — exit 1, silent. An observation pointing there is pointing at the
// user's ask, not proof of work, and this is what refuses it.
func TestT029_21_UserLineIsNotAToolResult(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	e.Run(proj, "s-029-17", "please MIGRATE the auth module", Turns("done",
		ToolResult("r1", "some tool output"),
	))
	path := e.TranscriptPath(proj, "s-029-17")

	promptLine := physicalLine(t, path, "please MIGRATE the auth module")
	if promptLine == 0 {
		t.Fatalf("the mock did not write the prompt to the transcript\n%s", readFile(t, path))
	}

	res := toolResultCmd(e, proj, path, promptLine)
	if res.Code != 1 {
		t.Fatalf("a user-message line exited %d, want 1 (not a tool_result):\n%s", res.Code, res.Output)
	}
	if strings.TrimSpace(res.Output) != "" {
		t.Fatalf("a non-tool_result line printed to stdout, which must be silent:\n%q", res.Output)
	}
}

// TestT029_22_AssistantLineIsNotAToolResult: an assistant turn — the agent narrating
// that it ran something — is not a tool_result. exit 1. This is the "narration is
// not proof" boundary the review rubric leans on, proven against the real record.
func TestT029_22_AssistantLineIsNotAToolResult(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	e.Run(proj, "s-029-18", "do it", Turns("done",
		Say("m1", "I ran the tests and they PASSEDNARRATION, trust me"),
	))
	path := e.TranscriptPath(proj, "s-029-18")

	assistantLine := physicalLine(t, path, "PASSEDNARRATION")
	if assistantLine == 0 {
		t.Fatalf("the mock did not write the assistant turn to the transcript\n%s", readFile(t, path))
	}

	res := toolResultCmd(e, proj, path, assistantLine)
	if res.Code != 1 {
		t.Fatalf("an assistant line exited %d, want 1 (narration is not a tool_result):\n%s", res.Code, res.Output)
	}
	if strings.TrimSpace(res.Output) != "" {
		t.Fatalf("an assistant line printed to stdout, which must be silent:\n%q", res.Output)
	}
}

// TestT029_23_NoLineIsAnError: tool-result with no --line (or a non-positive one) is
// the caller's mistake — a non-zero error on stderr, not "not a tool_result" (there
// is no line to classify). Distinct from exit 1 so a script tells the two apart.
func TestT029_23_NoLineIsAnError(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	e.Run(proj, "s-029-19", "anything", Turns("done"))
	path := e.TranscriptPath(proj, "s-029-19")

	res := e.CLIDirect(proj, "sr-session", "trajectory", "tool-result", "--path", path)
	if res.Code == 0 {
		t.Fatalf("tool-result with no --line exited 0, want non-zero:\n%s", res.Output)
	}
	if !res.Saw("--line must be a positive line number") {
		t.Fatalf("the error did not name the missing --line:\n%s", res.Output)
	}
}
