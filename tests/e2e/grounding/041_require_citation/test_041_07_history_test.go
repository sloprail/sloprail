package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The citations a file-guard's range is judged against at Stop, probed on ordinary
// workflows. A citation is a commit trailer, resolved against the session's own
// record, and grounds the range its commit is in. Every rule here is the
// file-guard afterCitationGuard (`memories/**`, a user citation, judged at Stop).
//
// REMOVED with the per-change citation history a file-guard used to keep (each
// pre-tool call recording the citation it rode on, and a change since charged to
// the agent when it carried none): the user's own edits between turns, a dirty
// tree at session start, checkout filters, branch switches, background jobs
// landing after a Stop, and a sub-agent's detached job (the old T041_39–46 and
// T041_49–51). None of that is a file-guard concern any more — a commit is what
// is judged, and a change nobody committed is refused for the commit
// (changeset/002) — and the citation a range needs is on its own commits, not
// reconstructed from what each tool call cited.

// T041_37 (P1): an uncited commit is refused — and the remedy the refusal prints,
// a `Sloprail-Cites-User` trailer on a commit, settles it: the range carries a
// citation after that, and a rule that refused it passes.
func TestT041_37_TheRemedySettlesAnUncitedChange(t *testing.T) {
	e, proj := guarded(t, afterCitationGuard)
	e.SetStopBlockCap(1)
	e.Run(proj, "s-041-37", prompt, Turns("done",
		Write("w1", "memories/a.md", "v1 nobody asked for"),
	).ThenCommit("write a note"))
	blocks := stopRefusal(e, proj, "s-041-37")
	if !strings.Contains(blocks, noCitation) || !strings.Contains(blocks, "Sloprail-Cites-User") {
		t.Fatalf("the uncited commit was not refused with the trailer remedy:\n%s", blocks)
	}
	seen := len(e.BlockingErrorsFrom(proj, "s-041-37", "Stop"))

	// The remedy: a commit in the same range that carries the citation.
	e.Run(proj, "s-041-37", "carry on", Turns("done",
		Write("w2", "memories/a.md", "v2 as asked"),
	).ThenCommit("as asked", harness.CitesUser("adopt a decision log")))
	if got := readProj(t, proj, "memories/a.md"); got != "v2 as asked" {
		t.Fatalf("the remedy did not land: %q", got)
	}
	if n := len(e.BlockingErrorsFrom(proj, "s-041-37", "Stop")); n != seen {
		t.Errorf("the printed remedy was still refused (%d refusals, had %d):\n%s", n, seen, stopRefusal(e, proj, "s-041-37"))
	}
}

// T041_38 (P2): a citation in ANOTHER pool does not ground a user-pool
// requirement: a commit citing a tool's output (here what an echo printed) for a
// file the rule wants the user's words for is refused.
func TestT041_38_AChangeCitedInAnotherPoolIsUncited(t *testing.T) {
	e, proj := guarded(t, afterCitationGuard)
	e.Run(proj, "s-041-38", prompt, Turns("done",
		Bash("b0", `echo ECHOED-7781`),
		Write("w1", "memories/a.md", "EVIL REWRITE"),
	).ThenCommit("rewrite the note", harness.CitesTool("ECHOED-7781")))
	if got := readProj(t, proj, "memories/a.md"); got != "EVIL REWRITE" {
		t.Fatalf("the rewrite did not land, so this tests nothing: %q", got)
	}
	if blocks := stopRefusal(e, proj, "s-041-38"); !strings.Contains(blocks, noCitation) {
		t.Errorf("a commit cited in the tool_result pool passed a user-pool requirement:\n%s", blocks)
	}
}

// T041_47 (P15): a cited sr-file named by the path of the engine's own sr-file is
// the same program, so the gate admits it like the bare name (it is dry-run) and
// the commit that carries the citation passes at Stop.
func TestT041_47_TheEnginesOwnSRFileByPath(t *testing.T) {
	e, proj := guarded(t, afterCitationGuard)
	byPath := filepath.Join(e.BinDir(), "sr-file")
	e.Run(proj, "s-041-47", prompt, Turns("done",
		Bash("b1", byPath+` write memories/a.md --cite:user 'adopt a decision log' --content 'by path'`),
	).ThenCommit("write a note", harness.CitesUser("adopt a decision log")))
	if got := readProj(t, proj, "memories/a.md"); got != "by path" {
		t.Fatalf("the write by path did not land: %q", got)
	}
	if blocks := stopRefusal(e, proj, "s-041-47"); blocks != "" {
		t.Errorf("a cited write by the engine's own sr-file path was refused at Stop:\n%s", blocks)
	}

	e2, proj2 := guardedPre(t)
	res := e2.Run(proj2, "s-041-47b", prompt, Turns("done",
		Bash("b1", filepath.Join(e2.BinDir(), "sr-file")+` write memories/a.md --cite:user 'adopt a decision log' --content 'by path'`),
	))
	if !e2.Exists(proj2, "memories/a.md") {
		t.Errorf("the gate refused a cited write by the engine's own sr-file path:\n%s", res.Output)
	}
}
