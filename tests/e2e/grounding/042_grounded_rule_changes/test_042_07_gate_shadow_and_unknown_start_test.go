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

// T042_15 (unknown start): when the session's first start was not kept, no protected
// disable is honoured, even one committed before the session. The rule stays on, and the
// range fails closed with its own reason rather than the rule going quiet.
func TestT042_15_UnknownSessionStartHonoursNoProtectedDisable(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.WriteFile(proj, ".sloprail/file-guard/demo/file-guard.yaml", demoYAML)
	e.WriteFile(proj, ".sloprail/file-guard/demo/check.sh", demoScript)
	e.DisablePluginGuardrail(proj, ruleName)
	e.GitInit(proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "the cited words cover the change"}`)
	const sess = "s-042-15-unknown"
	e.Run(proj, sess, "hello", Turns("done", Bash("b1", "true")))
	e.DeleteMeta(proj, sess, "session_start_commit")
	e.WriteFile(proj, ".sloprail/file-guard/demo/check.sh", demoLoosened)
	e.CommitAll(proj, "loosen the demo")
	res := e.StopNow(proj, sess, false)
	if !harness.Blocked(res) || !strings.Contains(res.Output, "grounded-rule-changes") {
		t.Fatalf("with no known session start the protected disable was honoured:\n%s", res.Output)
	}
}
