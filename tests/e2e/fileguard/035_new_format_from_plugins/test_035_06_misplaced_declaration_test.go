package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T035_06: the sloprail plugin's shipped misplaced-declaration guard. A
// structure written one level too high (.sloprail/structure.yaml) is never read
// by the engine — measured in the onboarding eval, where fresh agents did this
// and nothing told them. The shipped guard refuses the write before it lands and
// names where it belongs.
//
// The positive half — the same YAML at the RIGHT path — now also has to clear
// the shipped read-structure-gate-doc guard (writing the structure requires the
// skill AND its structure-gate.md page to have been read first), so this test
// reads both before writing, the same precondition a real author now meets.
// See T035_07 for read-structure-gate-doc's own dedicated coverage.
func TestT035_06_MisplacedDeclarationIsRefusedWithWhereItBelongs(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	structure := "allow:\n  - glob: \".sloprail/**\"\n  - glob: \"src/**\"\n"

	res := e.Run(proj, "s-035-06a", "write the structure", Turns("done",
		Write("w1", ".sloprail/structure.yaml", structure),
	))
	if !res.Refused() {
		t.Fatalf("a structure written to .sloprail/structure.yaml was not refused — the engine never reads it, "+
			"so the author is left believing a rule is in force:\n%s", res.Output)
	}
	if e.Exists(proj, ".sloprail/structure.yaml") {
		t.Errorf("the misplaced structure landed despite the refusal")
	}
	if !res.Saw(".sloprail/file-guard/structure.yaml") {
		t.Errorf("the refusal does not name where the structure belongs:\n%s", res.Output)
	}

	res = e.Run(proj, "s-035-06b", "load the skill, read its structure-gate page, then write the structure at its real path", Turns("done",
		Skill("s1", "authoring-guardrails"),
		ToolUse("r1", "Read", map[string]string{"file_path": harness.ShippedSkillFile(t, "structure-gate.md")}),
		Write("w1", ".sloprail/file-guard/structure.yaml", structure),
	))
	if res.Refused() {
		t.Fatalf("the structure at its real path was refused though the skill and its structure-gate page were both read:\n%s", res.Output)
	}
	if !e.Exists(proj, ".sloprail/file-guard/structure.yaml") {
		t.Errorf("the structure at its real path did not land")
	}
}

// T035_07: the sloprail plugin's shipped read-structure-gate-doc guard, on its
// own — refuses writing the structure gate until structure-gate.md was read,
// even once the skill itself was loaded (the property `files` exists for: a
// bare {skill} would have been satisfied by the skill alone).
func TestT035_07_ReadStructureGateDocRefusesUntilThePageIsRead(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	structure := "allow:\n  - glob: \".sloprail/**\"\n"

	res := e.Run(proj, "s-035-07", "load the skill but not its structure-gate page, then write the structure", Turns("done",
		Skill("s1", "authoring-guardrails"),
		Write("w1", ".sloprail/file-guard/structure.yaml", structure),
	))
	if !res.Refused() {
		t.Fatalf("the write landed though structure-gate.md was never read — loading the skill alone must not be enough:\n%s", res.Output)
	}
	if e.Exists(proj, ".sloprail/file-guard/structure.yaml") {
		t.Errorf("the write LANDED despite the files require being unmet")
	}
	if !res.Saw("structure-gate.md") {
		t.Errorf("the refusal does not name the missing page:\n%s", res.Output)
	}
}
