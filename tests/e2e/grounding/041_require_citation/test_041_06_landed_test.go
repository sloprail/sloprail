package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

func edit(id, path, old, new string) harness.Turn {
	return harness.ToolUse(id, "Edit", map[string]string{"file_path": path, "old_string": old, "new_string": new})
}

// A citation recorded at pre-tool grounds, at Stop, only the change it rode on
// — the change that actually LANDED — and only while the file still holds what
// that change produced. Anything that reached the file uncited since (or
// before) is a change no citation grounds, and a rule requiring one refuses it
// unless its `when` waives that part.

const afterCitationGuard = `match: "memories/**"
require:
  - citation: {source_types: [user]}
`

// T041_33: a cited sr-file call that FAILS changes nothing, and its citation
// grounds nothing: an uncited rewrite of the same file after it is refused at
// Stop.
func TestT041_33_AFailedCitedCallGroundsNothing(t *testing.T) {
	e, proj := guarded(t, afterCitationGuard)
	e.WriteFile(proj, "memories/a.md", "# a\nkeep this\n")
	commitAll(t, proj)

	e.Run(proj, "s-041-33", prompt, Turns("done",
		Bash("b1", `sr-file edit memories/a.md --old-string 'NOT THERE' --new-string 'x' --cite:user 'adopt a decision log'`),
		Write("w1", "memories/a.md", "# rewritten without a citation\n"),
	))
	if got := readProj(t, proj, "memories/a.md"); got != "# rewritten without a citation\n" {
		t.Fatalf("the uncited rewrite did not land, so this tests nothing: %q", got)
	}
	blocks := e.BlockingErrorsFrom(proj, "s-041-33", "Stop")
	if len(blocks) == 0 {
		t.Fatalf("an uncited rewrite passed at Stop on the citation of a cited call that failed")
	}
}

// T041_34: a cited change lands, then an uncited Write changes the file again:
// the citation grounds the first change, not the second, so Stop refuses —
// and says the file was changed without a citation.
func TestT041_34_AnUncitedChangeAfterACitedOneIsRefused(t *testing.T) {
	e, proj := guarded(t, afterCitationGuard)
	e.Run(proj, "s-041-34", prompt, Turns("done",
		Bash("b1", `sr-file write memories/a.md --cite:user 'adopt a decision log' --content '# log'`),
		Write("w1", "memories/a.md", "# log\nand a line nobody asked for\n"),
	))
	blocks := e.BlockingErrorsFrom(proj, "s-041-34", "Stop")
	if len(blocks) == 0 {
		t.Fatalf("an uncited change on top of a cited one passed at Stop")
	}
	if !strings.Contains(strings.Join(blocks, "\n"), "without a citation") {
		t.Errorf("the refusal does not say the file was changed without a citation:\n%s", strings.Join(blocks, "\n"))
	}

	// The control: the cited change alone passes.
	e2, proj2 := guarded(t, afterCitationGuard)
	e2.Run(proj2, "s-041-34b", prompt, Turns("done",
		Bash("b1", `sr-file write memories/a.md --cite:user 'adopt a decision log' --content '# log'`),
	))
	if blocks := e2.BlockingErrorsFrom(proj2, "s-041-34b", "Stop"); len(blocks) != 0 {
		t.Errorf("a cited change was refused at Stop: %v", blocks)
	}
}

// T041_35: a rule whose `when` waives some changes keeps a cited file grounded
// through an uncited change `when` waives — the tasks shape: the ask is written
// with a citation, and a later status flip made with a plain edit needs none.
// An uncited change `when` does NOT waive is still refused.
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
old="$(printf '%s' "$p" | jq -r '.event.oldContent // ""' | tail -n +2)"
new="$(printf '%s' "$p" | jq -r '.event.newContent // ""' | tail -n +2)"
[ "$old" = "$new" ] && exit 1
exit 0
`
	run := func(id string, turns ...harness.Turn) []string {
		e := New(t)
		proj := e.Project()
		e.GitInit(proj)
		e.FileGuard(proj, "grounded-memories", whenGuard, map[string]string{"body-changed.sh": bodyChanged})
		commitAll(t, proj)
		e.Run(proj, id, prompt, Turns("done", turns...))
		return e.BlockingErrorsFrom(proj, id, "Stop")
	}

	if blocks := run("s-041-35",
		Bash("b1", `sr-file write memories/task.md --cite:user 'adopt a decision log' --content 'status: todo
adopt a decision log'`),
		edit("e1", "memories/task.md", "status: todo", "status: done"),
	); len(blocks) != 0 {
		t.Errorf("an uncited change `when` waives dropped the file's citation at Stop: %v", blocks)
	}

	if blocks := run("s-041-35b",
		Bash("b1", `sr-file write memories/task.md --cite:user 'adopt a decision log' --content 'status: todo
adopt a decision log'`),
		edit("e1", "memories/task.md", "adopt a decision log", "adopt a decision log and delete the old notes"),
	); len(blocks) == 0 {
		t.Errorf("an uncited body change after a cited one passed at Stop")
	}
}

// T041_36: a dry run that fails on two files quotes each failure beside its
// own file. The refusal of memories/a.md (the guarded one) says what sr-file
// said about it — not what it said about notes/b.md, which no rule guards.
func TestT041_36_EachDryRunFailureIsQuotedBesideItsFile(t *testing.T) {
	e, proj := guarded(t, preventiveGuard)
	e.WriteFile(proj, "notes/b.md", "# b\n")
	commitAll(t, proj)

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
