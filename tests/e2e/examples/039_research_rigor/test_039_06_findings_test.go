package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The findings-need-depth gate: a Stop gate can refuse a turn, but not the ORDER
// inside it — real runs wrote the "Proposed approach" into NOTES.md first and
// read one more source file afterwards only to get past Stop. So while a
// #research run is open, a write of the research notes (the example's
// convention: Markdown in the project) is refused BEFORE it lands until the run
// has depth, with the same remedy the Stop gate gives.

const seedNotes = "# retry-helper — planning notes\n\nResearch before proposing.\n"

const proposal = "# retry-helper — planning notes\n\n## Proposed approach\n\nExponential backoff with jitter.\n"

// heldReason is the write-time lead-in of the shared depth refusal.
const heldReason = "Writing NOTES.md now would record this #research run's findings before the research has depth"

// notesProject is a research project whose NOTES.md is committed.
func notesProject(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e, proj := research(t)
	e.WriteFile(proj, "NOTES.md", seedNotes)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "notes")
	return e, proj
}

func notes(t *testing.T, proj string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(proj, "NOTES.md"))
	if err != nil {
		t.Fatalf("read NOTES.md: %v", err)
	}
	return string(b)
}

// T039_19: the proposal written into NOTES.md BEFORE the research has depth is
// refused before it lands — the file keeps its seed content — and the refusal
// says what is missing and what to do.
func TestT039_19_ProposalBeforeDepthRefusedBeforeItLands(t *testing.T) {
	e, proj := notesProject(t)
	src := sourceRepo(t, e, "retry-lib")
	dst := filepath.Join(scratch(t), "retry-lib")

	res := e.Run(proj, "s-039-19", "research retry", Turns("done",
		SayBash("m1", "Researching retry libraries. #research", "echo start"),
		Bash("b1", "git clone "+src+" "+dst),
		Read("r1", filepath.Join(dst, "README.md")),
		harness.Write("w1", filepath.Join(proj, "NOTES.md"), proposal),
	))

	if got := notes(t, proj); got != seedNotes {
		t.Fatalf("the proposal landed before the research had depth:\n%s\n%s", got, res.Output)
	}
	if !res.Refused() {
		t.Fatalf("the write was not refused:\n%s", res.Output)
	}
	for _, want := range []string{heldReason, "none of its source files", readMore(2, dst), "findings-need-depth"} {
		if !res.Saw(want) {
			t.Errorf("the write refusal is missing %q:\n%s", want, res.Output)
		}
	}
}

// T039_20: the same write AFTER the clone's source was read lands, and the turn
// ends admitted.
func TestT039_20_ProposalAfterDepthLands(t *testing.T) {
	e, proj := notesProject(t)
	src := sourceRepo(t, e, "retry-lib")
	dst := filepath.Join(scratch(t), "retry-lib")

	sess := "s-039-20"
	res := e.Run(proj, sess, "research retry", Turns("done",
		SayBash("m1", "Researching retry libraries. #research", "echo start"),
		Bash("b1", "git clone "+src+" "+dst),
		Read("r1", filepath.Join(dst, "lib", "retry.js")),
		Read("r2", filepath.Join(dst, "lib", "backoff.js")),
		harness.Write("w1", filepath.Join(proj, "NOTES.md"), proposal),
	))

	if got := notes(t, proj); got != proposal {
		t.Fatalf("the proposal did not land after the research had depth:\n%s\n%s", got, res.Output)
	}
	if blocks := e.BlockingErrors(proj, sess); len(blocks) != 0 {
		t.Errorf("a researched proposal was refused:\n%s", strings.Join(blocks, "\n"))
	}
}

// T039_21: with no #research declared, NOTES.md is an ordinary file — the write
// lands and nothing is refused.
func TestT039_21_NoResearchNotesWriteUnaffected(t *testing.T) {
	e, proj := notesProject(t)

	sess := "s-039-21"
	res := e.Run(proj, sess, "update notes", Turns("done",
		SayBash("m1", "Updating the planning notes.", "echo start"),
		harness.Write("w1", filepath.Join(proj, "NOTES.md"), proposal),
	))

	if got := notes(t, proj); got != proposal {
		t.Fatalf("an ordinary NOTES.md write did not land:\n%s\n%s", got, res.Output)
	}
	if blocks := e.BlockingErrors(proj, sess); len(blocks) != 0 {
		t.Errorf("an ordinary NOTES.md write was refused:\n%s", strings.Join(blocks, "\n"))
	}
}

// T039_22: the proposal cannot route around the refusal — not through a shell
// heredoc into NOTES.md, and not into a different Markdown file.
func TestT039_22_EvasionRefused(t *testing.T) {
	cases := []struct {
		name, file string
		turn       func(proj string) harness.Turn
	}{
		{"shell heredoc into NOTES.md", "NOTES.md", func(proj string) harness.Turn {
			return Bash("b2", "cat > NOTES.md <<'EOF'\n"+proposal+"EOF")
		}},
		{"shell append into NOTES.md", "NOTES.md", func(proj string) harness.Turn {
			return Bash("b2", "echo '## Proposed approach' >> NOTES.md")
		}},
		{"another Markdown file", "PROPOSAL.md", func(proj string) harness.Turn {
			return harness.Write("w2", filepath.Join(proj, "PROPOSAL.md"), proposal)
		}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, proj := notesProject(t)
			res := e.Run(proj, "s-039-22-"+string(rune('a'+i)), "research retry", Turns("done",
				SayBash("m1", "Researching retry libraries. #research", "echo start"),
				tc.turn(proj),
			))
			if tc.file == "NOTES.md" {
				if got := notes(t, proj); got != seedNotes {
					t.Fatalf("the proposal reached NOTES.md through the shell:\n%s\n%s", got, res.Output)
				}
			} else if e.Exists(proj, tc.file) {
				t.Fatalf("the proposal landed in %s instead:\n%s", tc.file, res.Output)
			}
			if !res.Saw("Writing " + tc.file + " now would record this #research run's findings") {
				t.Errorf("the refusal did not name the held write:\n%s", res.Output)
			}
		})
	}
}
