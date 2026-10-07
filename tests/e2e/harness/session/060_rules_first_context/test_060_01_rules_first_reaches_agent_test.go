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
	if !harness.HasCap(t, harness.CapRecordHoldsHookContext) {
		// This harness writes the context a start hook added nowhere in its transcript (the mock
		// hands it to the scripted agent out of band), so the record cannot show what the agent
		// was handed. What can be seen is the form `sr-session emit-context` writes it in, which
		// the harness reads it from.
		if strings.Contains(body, "sloprail is active in this project") {
			t.Errorf("a harness whose record holds no hook context carries the rules-first text in it:\n%s", body)
		}
		res := e.CLIDirectStdinEnv(proj, "the rules come before the work", e.SessionEnv(sess), "sr-session", "emit-context")
		if !strings.Contains(res.Output, `"additional_context"`) || !strings.Contains(res.Output, "the rules come before the work") {
			t.Errorf("emit-context did not hand the text over in the harness's own form:\n%s", res.Output)
		}
		return
	}
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
