package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// requirePluginSkillGate permits a write under memories/ only once the sloprail
// plugin's authoring-guardrails skill was read this session.
const requirePluginSkillGate = `on:
  - event: PreFileWrite
    match: event.path startsWith "memories/" and event.path endsWith ".md"
require:
  - skill: authoring-guardrails
`

// T048_01: the agent reads the plugin's skill from the INSTALLED copy (the cache a
// harness unpacks it into, not the marketplace source the engine resolves the plugin
// to) with a `cat`, and the guarded write then passes. A Codex onboarding eval refused
// every write after exactly this read. The read is a cat of the installed path on every
// harness; which path that is differs per harness (harness.InstalledPluginSkill).
// sr:proves checks/skill-requirement-reads-the-record
func TestT048_01_CatOfTheInstalledCopyPermitsWrite(t *testing.T) {
	e := harness.New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "require-plugin-skill", requirePluginSkillGate, nil)
	e.CommitAll(proj, "the guards")
	installed := e.InstalledPluginSkill("authoring-guardrails")

	res := e.Run(proj, "s-048-01", "cat the installed skill, then write a memory", harness.Turns("done",
		harness.Bash("b1", "cat "+installed),
		harness.Write("w1", "memories/idea.md", "# an idea"),
	))

	if res.Refused() {
		t.Fatalf("the write was refused though the installed copy of the skill (%s) was read:\n%s", installed, res.Output)
	}
	if !e.Exists(proj, "memories/idea.md") {
		t.Errorf("the write did not land though the skill was read")
	}
}

// T048_02: the control — without the read the same write is refused, naming the skill.
// sr:proves checks/skill-requirement-reads-the-record
func TestT048_02_NoReadStillRefuses(t *testing.T) {
	e := harness.New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "require-plugin-skill", requirePluginSkillGate, nil)
	e.CommitAll(proj, "the guards")

	res := e.Run(proj, "s-048-02", "write a memory without reading the skill", harness.Turns("done",
		harness.Write("w1", "memories/idea.md", "# an idea"),
	))

	if !res.Refused() || e.Exists(proj, "memories/idea.md") {
		t.Fatalf("the write landed though the skill was never read:\n%s", res.Output)
	}
}
