package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T042_18: a project cannot shadow the rule. A NEW `.sloprail/file-guard/grounded-rule-changes`
// of its own, with a match that selects nothing, needs no grounding as an added file, and
// without protection it would take the plugin rule's name and judge nothing, so the same
// commit could loosen a rule freely. The plugin's rule claims its name first.
// sr:proves loading/precedence-and-shadowing
func TestT042_18_AProjectRuleCannotShadowIt(t *testing.T) {
	e := NewUncited(t)
	proj := project(t, e)
	const sess = "s-042-18"
	// Written by a script, which no pre-write gate models, in one commit.
	write := `python3 -c "import os; os.makedirs('.sloprail/file-guard/grounded-rule-changes'); ` +
		`open('.sloprail/file-guard/grounded-rule-changes/file-guard.yaml','w').write('match: \\'nothing/**\\'\\nchecks:\\n  - script: ./check.sh\\n'); ` +
		`open('.sloprail/file-guard/grounded-rule-changes/check.sh','w').write('#!/bin/sh\\nexit 0\\n'); ` +
		`open('` + demoDir + `/check.sh','w').write('#!/bin/sh\\ncat >/dev/null\\n# nothing to see\\nexit 0\\n'); ` +
		`os.chmod('.sloprail/file-guard/grounded-rule-changes/check.sh', 0o755)"`
	e.Run(proj, sess, "loosen the demo rule so my notes land", Turns("done",
		Bash("w1", write),
		harness.Commit("c1", "shadow rule and loosen the demo"),
	))
	got, out := blocked(e, proj, sess)
	if !got {
		t.Fatalf("a project rule of the same name shadowed the plugin's, and an uncited loosening passed:\n%s", out)
	}
	e.Run(proj, sess, "cite it", Turns("done", harness.AmendLast("amend", "shadow rule and loosen the demo", harness.CitesUser("loosen the demo rule"))))
	if got, out := blocked(e, proj, sess); got {
		t.Fatalf("the cited change was refused:\n%s", out)
	}
}

// T042_19: a rule switched off in a config committed BEFORE the session began is the user's
// own decision and is honoured (the control for T042_15, where the same entries written
// during the session are not).
// sr:proves loading/protected-disable-needs-trusted-config
func TestT042_19_ADisableCommittedBeforeTheSessionIsHonoured(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.WriteFile(proj, ".sloprail/file-guard/demo/file-guard.yaml", demoYAML)
	e.WriteFile(proj, ".sloprail/file-guard/demo/check.sh", demoScript)
	if err := os.Chmod(proj+"/.sloprail/file-guard/demo/check.sh", 0o755); err != nil {
		t.Fatal(err)
	}
	e.DisablePluginGuardrail(proj, ruleName)
	e.GitInit(proj)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "SR042 the judge ran on a rule its project switched off"}`)
	e.Run(proj, "s-042-19", "loosen the demo rule", Turns("done",
		Bash("l1", `python3 -c "open('`+demoDir+`/check.sh','w').write('#!/bin/sh\ncat >/dev/null\nexit 0\n# loosened\n')"`),
	).ThenCommit("loosen the demo"))
	res := e.CheckRunRaw(proj, "s-042-19", e.RunBase("s-042-19"), "HEAD")
	if strings.Contains(res.Output, "grounded-rule-changes") || strings.Contains(res.Output, "SR042") {
		t.Fatalf("a rule disabled in the committed config was still enforced:\n%s", res.Output)
	}
}
