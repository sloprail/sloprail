package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T035_08: the FILE-GUARD half of misplaced-declaration, at Stop. A shell
// redirect is a write the gate cannot see beforehand, so a structure left one level
// too high settles, the agent commits it, and the guard refuses the turn naming
// where it belongs. Moving the declaration to its real path and committing that
// passes: the moved file is what the fixed range holds.
func TestT035_08_AMisplacedDeclarationCommittedIsRefusedAtStopThenPasses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	const session = "s-035-08"
	res := e.Run(proj, session, "write the structure one level too high", Turns("done",
		harness.Bash("b1", "mkdir -p .sloprail && printf 'allow:\\n  - glob: \".sloprail/**\"\\n' > .sloprail/structure.yaml"),
	).ThenCommit("the structure"))

	if !res.Refused() || !res.Saw(".sloprail/file-guard/structure.yaml") {
		t.Fatalf("a committed .sloprail/structure.yaml was not refused at Stop with where it belongs:\n%s", res.Output)
	}

	res = e.Run(proj, session, "move it to where the engine reads it", Turns("done",
		harness.Bash("b2", "mkdir -p .sloprail/file-guard && git mv .sloprail/structure.yaml .sloprail/file-guard/structure.yaml"),
	).ThenCommit("move the structure"))

	if res.Refused() {
		t.Fatalf("the declaration at its real path was still refused:\n%s", res.Output)
	}
}
