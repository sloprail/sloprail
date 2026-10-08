package e2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Every route an agent has to a rule's change leads to the same refusal, and the same
// grounded remedy passes it. The routes the first tests do not take: the Edit tool, a
// `sed -i`, a `git rm`, a rename out of `.sloprail/`, deleting config.yaml, and writing
// the rule's own name into `disabled:` with a writer no gate models.

const demoDir = ".sloprail/file-guard/demo"

func readDocs(t *testing.T, id string) []harness.Turn {
	t.Helper()
	return editScript(t, id, demoDir+"/check.sh", demoScript)[:3]
}

// T042_10: the Edit tool on a rule's script.
func TestT042_10_EditToolNeedsGrounding(t *testing.T) {
	e := NewUncited(t)
	proj := project(t, e)
	edit := harness.ToolUse("e1", "Edit", map[string]string{
		"file_path": proj + "/" + demoDir + "/check.sh", "old_string": "exit 0", "new_string": "# nothing to see\nexit 0"})
	refuseThenPass(t, e, proj, "s-042-10", "loosen the demo rule so my notes land",
		Turns("done", append(readDocs(t, "e"), edit)...), harness.CitesUser("loosen the demo rule"), advice...)
}

// T042_11: `sed -i`, which no gate can compute the result of.
func TestT042_11_SedInPlaceNeedsGrounding(t *testing.T) {
	e := NewUncited(t)
	proj := project(t, e)
	refuseThenPass(t, e, proj, "s-042-11", "loosen the demo rule so my notes land",
		Turns("done",
			harness.Skill("ss", "authoring-guardrails"),
			harness.ToolUse("sr", "Read", map[string]string{"file_path": harness.ShippedSkillFile(t, "file-guard.md")}),
			Bash("s1", "sed -i.bak 's/notes/everything/' "+demoDir+"/file-guard.yaml && rm "+demoDir+"/file-guard.yaml.bak")),
		harness.CitesUser("loosen the demo rule"), advice...)
}

// T042_12: `git rm` of a rule's folder.
func TestT042_12_GitRmNeedsGrounding(t *testing.T) {
	e := NewUncited(t)
	proj := project(t, e)
	refuseThenPass(t, e, proj, "s-042-12", "drop the demo rule, we no longer want it",
		Turns("done", Bash("g1", "git rm -rq "+demoDir)), harness.CitesUser("drop the demo rule"), advice...)
}

// T042_13: moving a rule's folder out of `.sloprail/` is a rename, not a deletion, and the
// rule it was is gone all the same.
func TestT042_13_MovingARuleOutNeedsGrounding(t *testing.T) {
	e := NewUncited(t)
	proj := project(t, e)
	refuseThenPass(t, e, proj, "s-042-13", "park the demo rule outside the project rules",
		Turns("done", Bash("m1", "mkdir -p archive && mv "+demoDir+" archive/demo")),
		harness.CitesUser("park the demo rule"), advice...)
}

// T042_14: deleting config.yaml. The pre-delete gate refuses it, and the same delete
// cited with sr-file lands.
func TestT042_14_DeletingConfigNeedsGrounding(t *testing.T) {
	e := New(t)
	proj := project(t, e)
	res := e.Run(proj, "s-042-14", "drop the config", Turns("done", Bash("d1", "rm .sloprail/config.yaml")))
	if !res.Refused() || !e.Exists(proj, ".sloprail/config.yaml") {
		t.Fatalf("deleting config.yaml with no grounding was not refused:\n%s", res.Output)
	}
	res = e.Run(proj, "s-042-14", "remove the config file entirely", Turns("done",
		Bash("d2", "sr-file delete .sloprail/config.yaml --cite:user 'remove the config file entirely'"),
	).ThenCommit("remove the config", harness.CitesUser("remove the config file entirely")))
	if res.Refused() || e.Exists(proj, ".sloprail/config.yaml") {
		t.Fatalf("the cited delete did not land:\n%s", res.Output)
	}
	if got, out := blocked(e, proj, "s-042-14"); got {
		t.Fatalf("the grounded delete was refused at Stop:\n%s", out)
	}
}

// T042_15: the rule cannot be switched off by writing its name into config.yaml with a
// writer no pre-write gate models. The entry is not honoured unless the config committed
// when the session began lists it, so the rule still refuses the change, and the same
// commit cited passes.
func TestT042_15_TheRuleCannotDisableItself(t *testing.T) {
	both := "  - sloprail/file-guard/grounded-rule-changes\\n  - sloprail/gate/grounded-rule-changes\\n"
	writers := map[string]string{
		"python": `python3 -c "open('.sloprail/config.yaml','a').write('` + both + `')"`,
		"yq":     `yq -i '.disabled += ["sloprail/file-guard/grounded-rule-changes", "sloprail/gate/grounded-rule-changes"]' .sloprail/config.yaml`,
	}
	for name, cmd := range writers {
		t.Run(name, func(t *testing.T) {
			if name == "yq" {
				if _, err := exec.LookPath("yq"); err != nil {
					t.Fatal("yq is required but not installed (brew install yq / https://github.com/mikefarah/yq)")
				}
			}
			e := NewUncited(t)
			proj := project(t, e)
			sess := "s-042-15-" + name
			e.Run(proj, sess, "get past the demo rule", Turns("done", Bash("w1", cmd)))
			e.Run(proj, sess, "commit it", Turns("done", harness.Commit("c1", "disable it")))
			got, out := blocked(e, proj, sess)
			if !got {
				cfg, _ := os.ReadFile(proj + "/.sloprail/config.yaml")
				t.Fatalf("the rule was switched off by its own config entry:\n%s\n%s", cfg, out)
			}
			e.Run(proj, sess, "cite it", Turns("done", harness.AmendLast("amend", "disable it", harness.CitesUser("get past the demo rule"))))
			if got, out := blocked(e, proj, sess); got {
				t.Fatalf("the cited change was refused:\n%s", out)
			}
		})
	}
}

// T042_16: a rule the agent added earlier in the session, and fixes later, needs no
// misfire to cite, even once the first commit passed a Stop. A rule that stood when the
// session began (the control, T042_02) still does.
func TestT042_16_ARuleAddedThisSessionCanBeFixed(t *testing.T) {
	e := New(t)
	proj := project(t, e)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "SR042 the judge ran on the agent's own new rule"}`)
	const sess = "s-042-16"
	e.Run(proj, sess, "add GET /invoices", Turns("done",
		harness.CommitFile("c1", ".sloprail/file-guard/invoices/file-guard.yaml", "match: \"invoices/*.md\"\nchecks:\n  - script: ./check.sh\n", "rule"),
		harness.CommitFile("c2", ".sloprail/file-guard/invoices/check.sh", demoScript, "its check"),
	))
	if got, out := blocked(e, proj, sess); got {
		t.Fatalf("adding a rule was refused:\n%s", out)
	}
	e.Run(proj, sess, "now fix it", Turns("done",
		harness.CommitFile("c3", ".sloprail/file-guard/invoices/check.sh", demoLoosened, "fix the check"),
	))
	if got, out := blocked(e, proj, sess); got || strings.Contains(out, "SR042") {
		t.Fatalf("fixing a rule added earlier in the session was refused or judged:\n%s", out)
	}
}
