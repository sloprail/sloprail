package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The fixture path a slop hook lands at — a NEW-format guardrail's own script
// directory, which authoring-slop's match selects.
const fixtureHookPath = ".sloprail/file-guard/mine/check.sh"

// buggyDerivableCreateHook is the EXACT 3x bug: it dispatches on kind and DOES
// consult resultKnown on the update branch — so the grep's "newContent without
// resultKnown" rule stays silent, because the word `resultKnown` appears — but on
// the CREATE branch it reads newContent assuming the create is always derivable.
// A NotebookEdit fresh-.ipynb PreFileCreate carries newContent "" and resultKnown
// false, so that branch reads the empty string as if it were the file.
const buggyDerivableCreateHook = `#!/bin/sh
input="$(cat)"
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
case "$kind" in
  PreFileCreate)
    # BUG: a create is assumed to always carry derivable bytes, so newContent is
    # read without consulting resultKnown. Wrong for a NotebookEdit fresh-.ipynb.
    new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    ;;
  PreFileUpdate)
    known="$(printf '%s' "$input" | jq -r '.event.resultKnown // false')"
    [ "$known" = "true" ] || exit 0
    new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    ;;
  *) exit 0 ;;
esac
[ -n "$new" ] || exit 0
exit 0
`

// correctDispatchHook is the pattern the corrected example scripts use: it
// consults resultKnown on BOTH Pre kinds and reads newContent only on the
// Post/derivable path, deferring to Post when the result is not known.
const correctDispatchHook = `#!/bin/sh
input="$(cat)"
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
case "$kind" in
  PostFileCreate|PostFileUpdate)
    new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    ;;
  PreFileCreate|PreFileUpdate)
    known="$(printf '%s' "$input" | jq -r '.event.resultKnown // false')"
    if [ "$known" != "true" ]; then
      exit 0
    fi
    new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    ;;
  *) exit 0 ;;
esac
[ -n "$new" ] || exit 0
exit 0
`

// T027_01: THE HEADLINE. The buggy derivable-create hook is PERMITTED by the grep
// (proven by running check-rules.sh directly) but REFUSED by the judge (a failing
// verdict is stubbed). This is the whole reason the judge exists: a subtlety the
// signature cannot decide.
func TestT027_01_JudgeRefusesTheDerivableCreateBugTheGrepMisses(t *testing.T) {
	e := New(t)
	proj := project(t, e)

	// FIRST, the fact that motivates the judge: the GREP alone permits this hook.
	// It names resultKnown (for the update branch), so check-rules.sh's
	// "newContent without resultKnown" rule does not fire — exit 0, permitted.
	if code := runGrepDirect(t, proj, buggyDerivableCreateHook); code != 0 {
		t.Fatalf("check-rules.sh (the grep) refused the buggy create with exit %d — the premise "+
			"that the grep MISSES this shape is wrong, so the judge test proves nothing new", code)
	}

	// The judge returns a FAILING verdict naming the rule. (The model is stubbed;
	// what is under test is that a failing verdict from the judge blocks the write,
	// where the grep let it through.)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "GUARDRAIL AUTHORING: the PreFileCreate branch reads .event.newContent without consulting .event.resultKnown, assuming a create is always derivable — breaks pre-kinds-consult-resultknown"}`)

	got := e.Run(proj, "s-027-01", "write a guardrail hook", Turns("done",
		Write("w1", fixtureHookPath, buggyDerivableCreateHook),
	))

	if !got.Refused() {
		t.Fatalf("the judge did not refuse the buggy derivable-create hook — the grep permitted it "+
			"(proven above), so nothing caught the exact 3x bug:\n%s", got.Output)
	}
	// The refusal carries the judge's reasoning (so the agent learns what to fix)
	// and names the guard as the plugin's own.
	if !got.Saw("resultKnown") {
		t.Errorf("the judge's reasoning did not reach the agent:\n%s", got.Output)
	}
	if !got.Saw("authoring-slop") {
		t.Errorf("the refusal does not name the guard:\n%s", got.Output)
	}
	if e.Exists(proj, fixtureHookPath) {
		t.Errorf("the buggy hook was written despite the refusal — the work was not prevented")
	}
}

// T027_02: the wiring proof. The judge's PROMPT carries BOTH the derivable-create
// rule (proving prepare assembled judge-rules/ into the array and the template
// iterated it) AND the offending script's own code (proving event.newContent
// reached the same prompt). Both present is what makes "a real model would catch
// it" credible — the model is handed the rule and the code together, which the
// grep's signature never brings into contact.
//
// A stubbed verdict alone cannot prove this: the renderer treats an undefined
// variable as empty, so a template reading additionalContext.rules renders fine
// whether prepare produced it or nothing. So this captures the rendered prompt and
// asserts the two facts a really-assembled prompt must carry.
func TestT027_02_TheRuleAndTheOffendingCodeBothReachTheJudgePrompt(t *testing.T) {
	e := New(t)
	proj := project(t, e)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-027-02", "write a guardrail hook", Turns("done",
		Write("w1", fixtureHookPath, buggyDerivableCreateHook),
	))

	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the judge never ran — no prompt captured (did the grep refuse first, or prepare fail?)")
	}
	// The derivable-create rule: present only if prepare read judge-rules/ AND the
	// template iterated additionalContext.rules. Its rule NAME and a distinctive
	// phrase from its body are the proof it was assembled, not just the frame.
	if !strings.Contains(prompt, "pre-kinds-consult-resultknown") {
		t.Errorf("the derivable-create rule name did not reach the judge prompt — prepare/template wiring is broken:\n%s", prompt)
	}
	if !strings.Contains(prompt, "NotebookEdit") {
		t.Errorf("the derivable-create rule's BODY did not reach the judge prompt (no 'NotebookEdit' phrase) — the array carried names but not bodies:\n%s", prompt)
	}
	// The offending code: present only if event.newContent reached the template.
	// The buggy PreFileCreate branch's comment is a line unique to the fixture.
	if !strings.Contains(prompt, "a create is assumed to always carry derivable bytes") {
		t.Errorf("the offending script's own code did not reach the judge prompt — event.newContent wiring is broken:\n%s", prompt)
	}
	// The DATA delimiter and clause frame the interpolated content.
	if !strings.Contains(prompt, "<judged-file") || !strings.Contains(prompt, "as DATA") {
		t.Errorf("the judged content was not framed as DATA:\n%s", prompt)
	}
}

// T027_03: the negative control. A CORRECT kind-dispatch hook — consulting
// resultKnown on BOTH Pre kinds, reading newContent only on Post/derivable —
// passes the grep AND the judge (a passing verdict is stubbed). Without this,
// T027_01 would pass against an engine that refused every hook, and the judge
// would be a rule that fires on taste.
func TestT027_03_CorrectDispatchHookPassesBothGrepAndJudge(t *testing.T) {
	e := New(t)
	proj := project(t, e)

	// The grep permits the correct hook.
	if code := runGrepDirect(t, proj, correctDispatchHook); code != 0 {
		t.Fatalf("check-rules.sh refused a CORRECT kind-dispatch hook with exit %d — the grep is over-firing", code)
	}

	// The judge passes it (the shape is correct, so a real judge would too).
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	// The shipped read-script-checks-doc guard (from the enabled plugin, not
	// this test's project-level authoring-slop copy) requires the skill and
	// its script-checks.md page read before any .sh write under file-guard/.
	got := e.Run(proj, "s-027-03", "write a correct guardrail hook", Turns("done",
		Skill("s1", "authoring-guardrails"),
		ToolUse("r1", "Read", map[string]string{"file_path": harness.ShippedSkillFile(t, "script-checks.md")}),
		Write("w1", fixtureHookPath, correctDispatchHook),
	))

	if got.Refused() {
		t.Fatalf("a correct kind-dispatch hook was refused — the judge is refusing a shape it should "+
			"accept (a false positive on the corrected pattern the example scripts use):\n%s", got.Output)
	}
	if !e.Exists(proj, fixtureHookPath) {
		t.Errorf("the permitted correct hook did not land")
	}
}
