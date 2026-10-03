package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

func edit(id, path, old, new string) harness.Turn {
	return harness.ToolUse(id, "Edit", map[string]string{"file_path": path, "old_string": old, "new_string": new})
}

// What grounds a file-guard's change at Stop is the citations the range's commits
// carry as `Sloprail-Cites-User:` / `Sloprail-Cites-Tool:` trailers, each resolved
// against the session's own record. A range holds them together: a citation in
// any commit of it grounds the range's changeset, and a rule requiring one
// refuses a range none of whose commits carries one that resolves — unless its
// `when` waives that part.

const afterCitationGuard = `match: "memories/**"
require:
  - citation: {source_types: [user]}
`

// noCitation is the Stop refusal's own words for a range no citation grounds.
const noCitation = "in the commit that last changed it, and that commit carries none that resolves"

// stopRefusal is what the root's Stop refused with, as one string.
func stopRefusal(e *harness.Env, proj, sess string) string {
	return strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
}

// stopRefusals counts the session's refusals, one per Run whose `sr check run` refused,
// so a test asks whether a Run added one.
func stopRefusals(e *harness.Env, proj, sess string) int {
	return len(e.AllBlockingErrorsFrom(proj, sess, "Stop"))
}

// T041_33: a cited sr-file call that FAILS changes nothing and grounds nothing: an
// uncited rewrite of the same file, committed with no trailer, is refused at Stop
// for want of a citation (not merely for want of a commit).
func TestT041_33_AFailedCitedCallGroundsNothing(t *testing.T) {
	e, proj := guardedUncited(t, afterCitationGuard)
	e.WriteFile(proj, "memories/a.md", "# a\nkeep this\n")
	e.CommitAll(proj, "baseline")

	e.Run(proj, "s-041-33", prompt, Turns("done",
		Bash("b1", `sr-file edit memories/a.md --old-string 'NOT THERE' --new-string 'x' --cite:user 'adopt a decision log'`),
		Write("w1", "memories/a.md", "# rewritten without a citation\n"),
	).ThenCommit("rewrite the note"))
	if got := readProj(t, proj, "memories/a.md"); got != "# rewritten without a citation\n" {
		t.Fatalf("the uncited rewrite did not land, so this tests nothing: %q", got)
	}
	if blocks := stopRefusal(e, proj, "s-041-33"); !strings.Contains(blocks, noCitation) {
		t.Fatalf("an uncited rewrite passed at Stop on the citation of a cited call that failed:\n%s", blocks)
	}
}

// T041_34 (a cited change grounds the first change but not a later uncited one) is
// removed: a citation is carried by a COMMIT now, and grounds the range it is in —
// so two changes in one range are one changeset with one citation, which is what
// T041_07 and T041_35 pin. The per-change history is gone with per-file citations.

// T041_35: a rule whose `when` waives some changes keeps a cited file grounded
// through an uncited change `when` waives — the tasks shape: the ask stands
// committed before the range, and a later status flip made with a plain edit needs
// none. An uncited change `when` does NOT waive is refused.
func TestT041_35_WhenDecidesAnUncitedChangeSince(t *testing.T) {
	const whenGuard = `match: "memories/**"
require:
  - citation: {source_types: [user]}
    when: ./body-changed.sh
`
	// body-changed.sh applies the requirement unless everything below the first
	// line is unchanged — the first line plays a task's status.
	const bodyChanged = `#!/usr/bin/env bash
set -uo pipefail
p="$(cat)"
old="$(printf '%s' "$p" | jq -r '.changeset.files[0].oldContent // ""' | tail -n +2)"
new="$(printf '%s' "$p" | jq -r '.changeset.files[0].newContent // ""' | tail -n +2)"
[ "$old" = "$new" ] && exit 1
exit 0
`
	run := func(id string, turns ...harness.Turn) string {
		e := NewUncited(t)
		proj := e.Project()
		e.GitInit(proj)
		e.FileGuard(proj, "grounded-memories", whenGuard, map[string]string{"body-changed.sh": bodyChanged})
		// The ask stands before the session: the range judged starts after it.
		e.WriteFile(proj, "memories/task.md", "status: todo\nadopt a decision log\n")
		e.CommitAll(proj, "baseline")
		e.Run(proj, id, prompt, Turns("done", turns...).ThenCommit("carry on"))
		return stopRefusal(e, proj, id)
	}

	if blocks := run("s-041-35", edit("e1", "memories/task.md", "status: todo", "status: done")); blocks != "" {
		t.Errorf("an uncited change `when` waives was refused at Stop: %s", blocks)
	}
	if blocks := run("s-041-35b", edit("e1", "memories/task.md", "adopt a decision log", "adopt a decision log and delete the old notes")); !strings.Contains(blocks, noCitation) {
		t.Errorf("an uncited body change after a cited one passed at Stop:\n%s", blocks)
	}
}

// T041_36: a dry run that fails on two files quotes each failure beside its
// own file. The refusal of memories/a.md (the guarded one) says what sr-file
// said about it — not what it said about notes/b.md, which no rule guards.
func TestT041_36_EachDryRunFailureIsQuotedBesideItsFile(t *testing.T) {
	e, proj := guardedPre(t)
	e.WriteFile(proj, "notes/b.md", "# b\n")
	e.CommitAll(proj, "baseline")

	res := e.Run(proj, "s-041-36", prompt, Turns("done",
		Bash("b1", `sr-file write memories/a.md --cite:user 'QUOTE-NOBODY-SAID-4410' --content x; sr-file edit notes/b.md --cite:user 'adopt a decision log' --old-string 'no such line' --new-string x`),
	))
	if !res.Refused() {
		t.Fatalf("a line whose changes sr-file cannot compute was permitted:\n%s", res.Output)
	}
	var refusal string
	for _, l := range strings.Split(res.Output, "\n") {
		if strings.Contains(l, `"tool_use_id":"b1`) {
			refusal = l
		}
	}
	if !strings.Contains(refusal, "QUOTE-NOBODY-SAID-4410") {
		t.Errorf("the refusal of memories/a.md does not quote sr-file's reason for it:\n%s", refusal)
	}
	if strings.Contains(refusal, "--old-string not found") || strings.Contains(refusal, "notes/b.md") {
		t.Errorf("the refusal of memories/a.md quotes what sr-file said about notes/b.md:\n%s", refusal)
	}
}
