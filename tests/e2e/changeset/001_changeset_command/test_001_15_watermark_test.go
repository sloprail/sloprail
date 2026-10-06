package e2e

import (
	"encoding/json"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// A rule's definition is part of every verdict's key: it is the hash of the rule's whole
// .sloprail root, which `sr-checks changeset` prints and `sr-checks run` stores with each run.
// These tests read the range back through `sr-checks changeset --base --head`.

const wmSession = "s-001-15"

// wmJudge adds a judge to the rule: a judge's verdict is what is stored under the rule's hash.
const (
	wmJudge  = "  - judge: ./rubric.md.j2\n"
	wmPrompt = ".git/judge-prompt"
)

// showRange runs `sr-checks changeset --rule <rule> --base <base> --head HEAD` in proj.
func showRange(t *testing.T, e *harness.Env, proj, rule, base string) (Shown, harness.Result) {
	t.Helper()
	res := e.CLIDirectEnv(proj, e.SessionEnv(wmSession), "sr-checks", "changeset", "--rule", rule, "--base", base, "--head", "HEAD")
	var s Shown
	if res.Code == 0 {
		if err := json.Unmarshal([]byte(res.Output), &s); err != nil {
			t.Fatalf("changeset printed unreadable JSON: %v\n%s", err, res.Output)
		}
	}
	return s, res
}

// watermarkRepo is a committed rule and one commit after it, with a started
// session, returning the rule's qualified name, hash, the rule's commit and the commit after.
func watermarkRepo(t *testing.T) (e *Env, proj, rule, hash, start, c1 string) {
	t.Helper()
	e = New(t)
	proj = e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/a.md", "one\n")
	e.CommitAll(proj, "before")
	e.FileGuard(proj, "size", docsRule("")+wmJudge, map[string]string{"check.sh": passingCheck, "rubric.md.j2": "WM-RUBRIC\n{{ change }}\n"})
	start = e.CommitAll(proj, "add the rule")
	e.InstallJudgeClaudeCapturing(proj, wmPrompt, `{"pass": true, "reasoning": "fine"}`)
	e.Run(proj, wmSession, "hello", Turns("done", Bash("b1", "true")))

	e.WriteFile(proj, "docs/a.md", "one\ntwo\n")
	c1 = e.CommitAll(proj, "first edit")
	first, res := showRange(t, e, proj, "size", start)
	if res.Code != 0 || first.Base != start {
		t.Fatalf("before any run: exit %d base %q\n%s", res.Code, first.Base, res.Output)
	}
	return e, proj, first.Rule, first.RuleHash, start, c1
}

// T001_17: a verdict is keyed on its input, not on the rule's definition. Edit the rule and the
// hash it is recorded under changes, but the pass reached under the older rule is still the
// answer for the same input: the judge is not asked again.
func TestT001_17_AnEditedRuleKeepsItsVerdicts(t *testing.T) {
	e, proj, _, hash, start, c1 := watermarkRepo(t)
	if r := e.CheckRunRaw(proj, wmSession, start, c1); r.Code != 0 || e.JudgeCalls(proj, wmPrompt, "") != 1 {
		t.Fatalf("premise: no pass at %s under %s (exit %d):\n%s", c1, hash, r.Code, r.Output)
	}
	e.WriteFile(proj, ".sloprail/file-guard/size/check.sh", passingCheck+"# edited\n")

	got, res := showRange(t, e, proj, "size", start)
	if res.Code != 0 {
		t.Fatalf("exit %d:\n%s", res.Code, res.Output)
	}
	if got.RuleHash == hash {
		t.Fatalf("the edit left the rule's recorded hash unchanged")
	}
	e.CheckRunRaw(proj, wmSession, start, c1)
	if n := e.JudgeCalls(proj, wmPrompt, ""); n != 1 {
		t.Fatalf("the pass reached under the older rule (%s) was judged again under %s (%d judge calls, want 1)", hash, got.RuleHash, n)
	}
}

// T001_19: a rule's identity is its whole .sloprail root, not its folder alone. A
// shared lib edited OUTSIDE the rule's own folder changes the hash, and commits made
// after a pass are judged by the new rule — and a range based at the pass holds only
// the commits after it: the work approved before is not judged again.
func TestT001_19_AnEditOutsideTheRuleFolderButInsideSloprailChangesTheHashNotTheWatermark(t *testing.T) {
	e, proj, _, hash, start, c1 := watermarkRepo(t)
	if r := e.CheckRunRaw(proj, wmSession, start, c1); r.Code != 0 || e.JudgeCalls(proj, wmPrompt, "") != 1 {
		t.Fatalf("premise: no pass at %s under %s (exit %d):\n%s", c1, hash, r.Code, r.Output)
	}

	e.WriteFile(proj, ".sloprail/lib/shared.sh", "#!/bin/sh\n# what rules share\n")
	e.CommitAll(proj, "edit the shared lib")
	e.WriteFile(proj, "docs/a.md", "one\ntwo\nthree\n")
	head := e.CommitAll(proj, "third edit")

	got, res := showRange(t, e, proj, "size", c1)
	if res.Code != 0 {
		t.Fatalf("exit %d:\n%s", res.Code, res.Output)
	}
	if got.RuleHash == hash {
		t.Fatalf("editing .sloprail/lib/shared.sh left the rule's hash unchanged")
	}
	if got.Base != c1 || got.Head != head {
		t.Fatalf("range = %s..%s, want %s..%s", got.Base, got.Head, c1, head)
	}
	files := got.Payload.Changeset.Files
	if len(files) != 1 || files[0].Path != "docs/a.md" || files[0].OldContent != "one\ntwo\n" || files[0].NewContent != "one\ntwo\nthree\n" {
		t.Fatalf("files = %+v, want only the change made after the pass", files)
	}
	if n := len(got.Payload.Changeset.Commits); n != 2 {
		t.Fatalf("commits = %+v, want the lib edit and the third edit", got.Payload.Changeset.Commits)
	}
	if r := e.CheckRunRaw(proj, wmSession, c1, "HEAD"); r.Code != 0 || e.JudgeCalls(proj, wmPrompt, "") != 2 {
		t.Fatalf("the commits after the pass were not judged by the new rule (exit %d, %d judge calls):\n%s", r.Code, e.JudgeCalls(proj, wmPrompt, ""), r.Output)
	}
}
