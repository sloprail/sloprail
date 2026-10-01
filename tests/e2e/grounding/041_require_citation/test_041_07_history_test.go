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

	// The remedy the refusal prints: amend the uncited commit with the trailer. A
	// later commit that cites does not retroactively ground the earlier one.
	refusal := stopRefusal(e, proj, "s-041-37")
	e.Run(proj, "s-041-37", "carry on", Turns("done", harness.RefusalCommand(t, "fix", refusal, "git commit --amend", "adopt a decision log")))
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

// T041_34: a cited change lands, then an uncited commit changes the file again: the
// citation grounds the commit it is in, not the later one, so Stop refuses and names
// the file. The control: the cited change alone passes. A cited commit ON TOP of an
// uncited one does not ground the file: every commit that changed it must cite.
func TestT041_34_AnUncitedChangeAfterACitedOneIsRefused(t *testing.T) {
	e, proj := guarded(t, afterCitationGuard)
	e.Run(proj, "s-041-34", prompt, Turns("done",
		Write("w1", "memories/a.md", "# log\n"),
		harness.Commit("c1", "write the log", harness.CitesUser("adopt a decision log")),
		Write("w2", "memories/a.md", "# log\nand a line nobody asked for\n"),
		harness.Commit("c2", "tidy"),
	))
	blocks := stopRefusal(e, proj, "s-041-34")
	if !strings.Contains(blocks, noCitation) || !strings.Contains(blocks, "memories/a.md") {
		t.Fatalf("an uncited change on top of a cited one passed at Stop, or the refusal does not name the file:\n%s", blocks)
	}

	e2, proj2 := guarded(t, afterCitationGuard)
	e2.Run(proj2, "s-041-34b", prompt, Turns("done", Write("w1", "memories/a.md", "# log\n")).
		ThenCommit("write the log", harness.CitesUser("adopt a decision log")))
	if blocks := stopRefusal(e2, proj2, "s-041-34b"); blocks != "" {
		t.Errorf("a cited change was refused at Stop: %s", blocks)
	}

	e3, proj3 := guarded(t, afterCitationGuard)
	e3.Run(proj3, "s-041-34c", prompt, Turns("done",
		Write("w1", "memories/a.md", "# log\n"),
		harness.Commit("c1", "write the log"),
		Write("w2", "memories/a.md", "# log\nas asked\n"),
		harness.Commit("c2", "as asked", harness.CitesUser("adopt a decision log")),
	))
	if blocks := stopRefusal(e3, proj3, "s-041-34c"); !strings.Contains(blocks, noCitation) || !strings.Contains(blocks, "memories/a.md") {
		t.Errorf("a cited commit on top of an uncited one grounded the file (every commit that changed it must cite):\n%s", blocks)
	}
}

// T041_53: citations are attributed PER FILE. Two files are changed in two commits and
// only the first commit cites: the rule refuses, naming the second file and not the
// first — one citation in the range does not ground a file it did not ride on. Citing
// the second file's own commit (amended) passes.
func TestT041_53_ACitationGroundsOnlyTheFilesItsCommitChanged(t *testing.T) {
	e, proj := guarded(t, afterCitationGuard)
	e.Run(proj, "s-041-53", prompt, Turns("done",
		Write("w1", "memories/a.md", "# a\n"),
		harness.Commit("c1", "write a", harness.CitesUser("adopt a decision log")),
		Write("w2", "memories/b.md", "# b\n"),
		harness.Commit("c2", "write b"),
	))
	blocks := stopRefusal(e, proj, "s-041-53")
	if !strings.Contains(blocks, noCitation) || !strings.Contains(blocks, "memories/b.md") {
		t.Fatalf("an uncited second file was not refused by name:\n%s", blocks)
	}
	if strings.Contains(blocks, "memories/a.md") {
		t.Errorf("the refusal names the cited file:\n%s", blocks)
	}
	seen := len(e.BlockingErrorsFrom(proj, "s-041-53", "Stop"))

	e.Run(proj, "s-041-53", "cite the second too", Turns("done",
		harness.RefusalCommand(t, "fix", blocks, "git commit --amend", "adopt a decision log"),
	))
	if n := len(e.BlockingErrorsFrom(proj, "s-041-53", "Stop")); n != seen {
		t.Errorf("citing the second file's commit was still refused (%d refusals, had %d):\n%s", n, seen, stopRefusal(e, proj, "s-041-53"))
	}
}

// T041_70 (issue #134): a touch-commit does not wash a citation. An uncited
// substantive commit X changes a file; a later whitespace-only commit Y carrying a
// generic trailer does not ground it (a whitespace-only commit grounds nothing, and
// X is still uncited). The same content cited in X itself passes, with Y uncited.
func TestT041_70_AWhitespaceCommitWithATrailerGroundsNothing(t *testing.T) {
	e, proj := guarded(t, afterCitationGuard)
	e.Run(proj, "s-041-70", prompt, Turns("done",
		Write("w1", "memories/a.md", "# log\nthe decision\n"),
		harness.Commit("x", "write the decision"),
		Write("w2", "memories/a.md", "# log\n\nthe decision  \n"),
		harness.Commit("y", "touch", harness.CitesUser("adopt a decision log")),
	))
	blocks := stopRefusal(e, proj, "s-041-70")
	if !strings.Contains(blocks, noCitation) || !strings.Contains(blocks, "memories/a.md") {
		t.Fatalf("a whitespace-only commit with a trailer washed the earlier uncited change:\n%s", blocks)
	}

	e2, proj2 := guarded(t, afterCitationGuard)
	e2.Run(proj2, "s-041-70b", prompt, Turns("done",
		Write("w1", "memories/a.md", "# log\nthe decision\n"),
		harness.Commit("x", "write the decision", harness.CitesUser("adopt a decision log")),
		Write("w2", "memories/a.md", "# log\n\nthe decision  \n"),
		harness.Commit("y", "tidy whitespace"),
	))
	if blocks := stopRefusal(e2, proj2, "s-041-70b"); blocks != "" {
		t.Errorf("the change cited in its own commit was refused because of a later whitespace-only commit:\n%s", blocks)
	}
}
