package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The refusal for a file no commit grounds is something an agent has to ACT on in one
// move. Measured in the evals, agents that had already cited a file with `sr-file --cite`
// thrashed for ten refusals: the refusal said "<exact quote>" and never that the quote
// they had was the one to paste. So it hands back the quotes the session recorded for
// the files, as the exact trailer, and gives one command to undo the whole range.

const userCiteGuard = `match: "memories/**"
require:
  - citation: {source_types: [user]}
`

// T041_60: a file written with a cited sr-file call and committed without the trailer is
// refused, and the refusal lists the quote already recorded for it as the exact trailer
// line and builds its amend from that quote. Running that command grounds the file.
func TestT041_60_TheRefusalHandsBackTheQuoteAlreadyRecorded(t *testing.T) {
	const ask = "adopt a decision log"
	const sess = "s-041-60"
	e, proj := guarded(t, userCiteGuard)

	e.Run(proj, sess, ask, Turns("done",
		Bash("w1", "sr-file write memories/a.md --cite:user 'adopt a decision log' <<'EOF'\n# a\nEOF"),
	).ThenCommit("write a"))
	refusal := stopRefusal(e, proj, sess)
	if got := harness.UngroundedFiles(refusal); got != "memories/a.md" {
		t.Fatalf("premise: the refusal names %q, want memories/a.md:\n%s", got, refusal)
	}
	for _, want := range []string{
		"Quotes already recorded for these files this session",
		"memories/a.md: Sloprail-Cites-User: adopt a decision log",
		"git commit --amend --no-edit --trailer 'Sloprail-Cites-User: adopt a decision log'",
		"-m 'Sloprail-Cites-User: adopt a decision log'",
	} {
		if !strings.Contains(refusal, want) {
			t.Errorf("the refusal lacks %q:\n%s", want, refusal)
		}
	}
	// The commands it gives carry the recorded quote, not the placeholder.
	if strings.Contains(refusal, "-m 'Sloprail-Cites-User: <exact quote>'") || strings.Contains(refusal, "--trailer 'Sloprail-Cites-User: <exact quote>'") {
		t.Errorf("a command still leaves a placeholder where the recorded quote belongs:\n%s", refusal)
	}
	refused := len(e.StopContinuations(proj, sess))

	// The line as printed, with nothing filled in by the agent.
	e.Run(proj, sess, "go on", Turns("done", harness.RefusalCommand(t, "fix", refusal, "git commit --amend", "")))
	if got := len(e.StopContinuations(proj, sess)); got != refused {
		t.Fatalf("the refusal's own command did not ground the file (%d refusals, had %d):\n%s", got, refused, stopRefusal(e, proj, sess))
	}
}

// T041_61: the quote a SUB-AGENT recorded for a file (a cited sr-file call in the shared
// tree) is listed too, in the pool it resolved in: a tool's output gives the tool trailer.
func TestT041_61_AQuoteASubagentRecordedIsListedToo(t *testing.T) {
	const guard = `match: "memories/**"
require:
  - citation: {source_types: [tool_result]}
`
	const sess = "s-041-61"
	e, proj := guarded(t, guard)
	sub := subagentScript(t, harness.Turns("sub done",
		Bash("sb1", "echo 'build finished: CITEUX-7310 green'"),
		Bash("sb2", "sr-file write memories/a.md --cite:tool_result 'CITEUX-7310 green' <<'EOF'\n# a\nEOF"),
		harness.Commit("sb3", "write a"),
	))
	e.Run(proj, sess, prompt, Turns("done", harness.Dispatch("d1", "write it down", sub, "")))
	refusal := stopRefusal(e, proj, sess)
	if got := harness.UngroundedFiles(refusal); got != "memories/a.md" {
		t.Fatalf("premise: the refusal names %q, want memories/a.md:\n%s", got, refusal)
	}
	if want := "memories/a.md: Sloprail-Cites-Tool: CITEUX-7310 green"; !strings.Contains(refusal, want) {
		t.Fatalf("the refusal does not list the quote the sub-agent recorded (%q):\n%s", want, refusal)
	}
	refused := len(e.StopContinuations(proj, sess))

	e.Run(proj, sess, "go on", Turns("done", harness.RefusalCommand(t, "fix", refusal, "git commit --amend", "")))
	if got := len(e.StopContinuations(proj, sess)); got != refused {
		t.Fatalf("the refusal's own command did not ground the file (%d refusals, had %d):\n%s", got, refused, stopRefusal(e, proj, sess))
	}
}

// T041_62: undoing the whole range is ONE command the refusal gives, and it needs no
// citation: the file is then exactly as it was at the base, so there is nothing to
// ground. A range whose net change is nil is not refused, however uncited its commits.
func TestT041_62_RevertingTheRangeNeedsNoCitation(t *testing.T) {
	const sess = "s-041-62"
	e, proj := guarded(t, userCiteGuard)
	e.WriteFile(proj, "memories/a.md", "# a\nkeep this\n")
	e.CommitAll(proj, "baseline file")

	e.Run(proj, sess, prompt, Turns("done",
		Write("w1", "memories/a.md", "# a\nchanged once\n"),
	).ThenCommit("first"))
	e.Run(proj, sess, "again", Turns("done",
		Write("w2", "memories/a.md", "# a\nchanged twice\n"),
	).ThenCommit("second"))
	refusal := stopRefusal(e, proj, sess)
	if got := harness.UngroundedFiles(refusal); got != "memories/a.md" {
		t.Fatalf("premise: the refusal names %q, want memories/a.md:\n%s", got, refusal)
	}
	if !strings.Contains(refusal, "git revert --no-commit ") || !strings.Contains(refusal, "..HEAD && git commit --no-edit") {
		t.Fatalf("the refusal does not give the one command that undoes the range:\n%s", refusal)
	}
	refused := len(e.StopContinuations(proj, sess))

	e.Run(proj, sess, "undo it", Turns("done", harness.RefusalCommand(t, "undo", refusal, "git revert --no-commit", "")))
	if got := len(e.StopContinuations(proj, sess)); got != refused {
		t.Fatalf("a range whose net change is nil was refused (%d refusals, had %d):\n%s", got, refused, stopRefusal(e, proj, sess))
	}
}
