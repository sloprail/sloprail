package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T060_01: the plugin's standing instruction (rules-first.md) reaches the agent at
// session start, on whichever harness runs the test.
//
// The plugin's SessionStart hook wrapper hands its start-up text to
// `sr-session emit-context`, which writes it in the running harness's own form
// (harness.RenderHook): Claude Code and Codex read the hookSpecificOutput
// additionalContext document, Cursor reads {"additional_context"}. A form the harness
// ignores would leave the agent without the instruction while every hook "succeeded",
// so the observable here is the session's own record of what the agent was handed (the
// mock files it as the real harness does), not the hook's exit status.
func TestT060_01_RulesFirstTextReachesTheAgent(t *testing.T) {
	harness.RequireCap(t, harness.CapRecordHoldsHookContext)
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	const sess = "s-060-01"
	e.Run(proj, sess, "do a thing", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	record, err := os.ReadFile(e.TranscriptPath(proj, sess))
	if err != nil {
		t.Fatalf("the session left no record: %v", err)
	}
	body := string(record)
	for _, want := range []string{
		"sloprail is active in this project",
		"the rules come before the work",
		"authoring-guardrails",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the session's record does not carry the rules-first text %q; the agent was never handed it", want)
		}
	}
}
