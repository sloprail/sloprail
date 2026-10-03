package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// grounded_rule_changes: the sloprail plugin's shipped file-guard that refuses a change
// to what already stands in a project's `.sloprail/` unless it is grounded in the user's
// words or in a tool output showing the rule misfiring.
//
// The rule is the plugin's, in force in a project that copied nothing. Every other
// shipped authoring file-guard is switched off in the initial commit
// (WithOnlyShippedFileGuard), so what these tests see is this rule alone. The judge's
// verdict is a stub: what is under test is the requirement, the deterministic check and
// the wiring, and each refusal is followed by the grounded remedy that passes.
const ruleName = "sloprail/file-guard/grounded-rule-changes"

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

var (
	Turns = harness.Turns
	Write = harness.Write
	Bash  = harness.Bash
)

// New is an environment where only this rule of the plugin's authoring file-guards is on.
func New(t *testing.T) *harness.Env {
	return harness.New(t, harness.WithOnlyShippedFileGuard(ruleName))
}

const (
	demoYAML   = "match: \"notes/*.md\"\nchecks:\n  - script: ./check.sh\n"
	demoScript = "#!/bin/sh\ncat >/dev/null\nexit 0\n"
	// The loosened script: the same rule, no longer checking anything the old one did.
	demoLoosened = "#!/bin/sh\ncat >/dev/null\n# nothing to see\nexit 0\n"
)

// project stands up a project that already has one rule of its own, committed before
// the session begins: the "what already stands" every case below changes.
func project(t *testing.T, e *harness.Env) string {
	t.Helper()
	proj := e.Project()
	e.WriteFile(proj, ".sloprail/file-guard/demo/file-guard.yaml", demoYAML)
	e.WriteFile(proj, ".sloprail/file-guard/demo/check.sh", demoScript)
	if err := os.Chmod(proj+"/.sloprail/file-guard/demo/check.sh", 0o755); err != nil {
		t.Fatal(err)
	}
	e.GitInit(proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "the cited words cover the change"}`)
	return proj
}

// shq single-quotes s for a POSIX shell, so a Bash turn passes it verbatim.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// editScript is the agent changing a rule's script: the plugin's own gates first want the
// authoring skill and its script-checks pages read, as they would of any author, so the
// turns that read them come first.
func editScript(t *testing.T, id, path, content string) []harness.Turn {
	t.Helper()
	return []harness.Turn{
		harness.Skill(id+"s", "authoring-guardrails"),
		harness.ToolUse(id+"r1", "Read", map[string]string{"file_path": harness.ShippedSkillFile(t, "script-checks.md")}),
		harness.ToolUse(id+"r2", "Read", map[string]string{"file_path": harness.ShippedSkillFile(t, "check-template.sh")}),
		Write(id, path, content),
	}
}

// blocked runs `sr check run` over what the session committed and says whether this rule
// refused it, with the refusal's text.
func blocked(e *harness.Env, proj, sess string) (bool, string) {
	res := e.CheckRunRaw(proj, sess, e.RunBase(sess), "HEAD")
	return res.Code != 0 && strings.Contains(res.Output, "grounded-rule-changes"), res.Output
}

// refuseThenPass is the shape of every case: the agent makes the change (`change`, which
// may end with a tool's output, after which the mock ends the turn) and commits it with no
// grounding, and the rule refuses; the commit is then amended to carry the given trailer
// and the same session passes.
func refuseThenPass(t *testing.T, e *harness.Env, proj, sess, ask string, change harness.Scenario, cite string, wantRefusal ...string) {
	t.Helper()
	e.Run(proj, sess, ask, change)
	e.Run(proj, sess, "commit it", Turns("done", harness.Commit("c1", "change the rule")))
	got, out := blocked(e, proj, sess)
	if !got {
		t.Fatalf("an ungrounded change to the project's rules was not refused:\n%s", out)
	}
	for _, w := range wantRefusal {
		if !strings.Contains(out, w) {
			t.Errorf("the refusal does not say %q:\n%s", w, out)
		}
	}
	e.Run(proj, sess, "cite it", Turns("done", harness.AmendLast("amend", "change the rule", cite)))
	if got, out := blocked(e, proj, sess); got {
		t.Fatalf("the change was still refused once its commit carried the grounding:\n%s", out)
	}
}

// NewUncited is New with the commit-time sloprail/gate/cite-before-commit switched off, for a
// scenario whose subject is what Stop or `sr-checks run` does with a commit that carries no (or
// no resolving) citation: with the gate on, the agent could not make that commit at all. The gate
// itself is exercised in tests/e2e/gate/058_cite_before_commit.
func NewUncited(t *testing.T) *harness.Env {
	return harness.New(t, harness.WithOnlyShippedFileGuard(ruleName), harness.WithoutShipped("sloprail/gate/cite-before-commit"))
}
