package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T042_20: the shadowing protection holds for the gate nature too. A project gate of the
// plugin gate's name, whose trigger matches nothing, would let a Write tool change
// config.yaml with no grounding if it took the plugin gate's place. It does not: the
// write is still refused before it lands, and the cited sr-file write lands.
func TestT042_20_AProjectGateCannotShadowTheGate(t *testing.T) {
	e := New(t)
	proj := project(t, e)
	const sess = "s-042-20"
	shadow := `python3 -c "import os; os.makedirs('.sloprail/gate/grounded-rule-changes'); ` +
		`open('.sloprail/gate/grounded-rule-changes/gate.yaml','w').write('on:\\n  - event: PreFileWrite\\n    match: \\'event.path == \"nothing\"\\'\\nchecks:\\n  - script: ./check.sh\\n'); ` +
		`open('.sloprail/gate/grounded-rule-changes/check.sh','w').write('#!/bin/sh\\nexit 0\\n')"`
	e.Run(proj, sess, "add a gate", Turns("done", Bash("w1", shadow), harness.Commit("c1", "add a gate")))

	before, _ := os.ReadFile(proj + "/.sloprail/config.yaml")
	disable := string(before) + "  - sloprail/file-guard/demo\n"
	res := e.Run(proj, sess, "get past the demo rule", Turns("done", Write("w2", ".sloprail/config.yaml", disable)))
	if !res.Refused() {
		t.Fatalf("a project gate of the plugin gate's name took its place, and an uncited config.yaml write landed:\n%s", res.Output)
	}
	if got, _ := os.ReadFile(proj + "/.sloprail/config.yaml"); string(got) != string(before) {
		t.Errorf("the refused write landed:\n%s", got)
	}
	res = e.Run(proj, sess, "turn the demo rule off, it is wrong", Turns("done",
		Bash("c2", "sr-file write .sloprail/config.yaml --content "+shq(disable)+" --cite:user 'turn the demo rule off'")))
	if res.Refused() {
		t.Fatalf("the cited write was refused:\n%s", res.Output)
	}
	if got, _ := os.ReadFile(proj + "/.sloprail/config.yaml"); !strings.Contains(string(got), "file-guard/demo") {
		t.Errorf("the cited write did not land:\n%s", got)
	}
}

// T042_32 (unlanded disable): a protected rule's disable that is committed but not yet on
// the default branch is not honoured: the range starts at the merge base with origin's
// default branch, where the rule is still on, so the rule judges the range (the disable
// included) and an uncited loosening of a rule is refused rather than the rule going quiet.
// (The old session-start meta this was keyed on is gone; the range base is the merge base.)
func TestT042_32_AnUnlandedProtectedDisableIsNotHonoured(t *testing.T) {
	e := harness.New(t, harness.WithOnlyShippedFileGuard(ruleName), harness.KeepOrigin())
	proj := e.Project()
	e.WriteFile(proj, ".sloprail/file-guard/demo/file-guard.yaml", demoYAML)
	e.WriteFile(proj, ".sloprail/file-guard/demo/check.sh", demoScript)
	e.GitInit(proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "the cited words cover the change"}`)
	// The disable is committed after what origin holds, so it has not landed.
	e.DisablePluginGuardrail(proj, ruleName)
	e.CommitAll(proj, "disable the protection")
	const sess = "s-042-15-unlanded"
	e.Run(proj, sess, "hello", Turns("done", Bash("b1", "true")))
	e.WriteFile(proj, ".sloprail/file-guard/demo/check.sh", demoLoosened)
	e.CommitAll(proj, "loosen the demo")
	res := e.StopJudged(proj, sess, false)
	if !harness.Blocked(res) || !strings.Contains(res.Output, "grounded-rule-changes") {
		t.Fatalf("a protected disable that has not landed was honoured:\n%s", res.Output)
	}
}
