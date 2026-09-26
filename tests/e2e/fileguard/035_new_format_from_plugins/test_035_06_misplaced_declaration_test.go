package e2e

import "testing"

// T035_06: the sloprail plugin's shipped misplaced-declaration guard. A
// structure written one level too high (.sloprail/structure.yaml) is never read
// by the engine — measured in the onboarding eval, where fresh agents did this
// and nothing told them. The shipped guard refuses the write before it lands and
// names where it belongs; the same YAML at the right path lands.
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

	res = e.Run(proj, "s-035-06b", "write the structure", Turns("done",
		Write("w1", ".sloprail/file-guard/structure.yaml", structure),
	))
	if res.Refused() {
		t.Fatalf("the structure at its real path was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, ".sloprail/file-guard/structure.yaml") {
		t.Errorf("the structure at its real path did not land")
	}
}
