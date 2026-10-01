package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// A requirement and its `when` are evaluated PER SUBJECT: by default one selected file,
// `subject.files`, with the whole changeset in the payload as context. `when` runs once
// per file and the requirement applies only to the files it applies to.

// T041_40: a `when` that reads the whole changeset as context still works. This one
// applies to a file only when the changeset ALSO touches memories/flag.md (context), and
// never to flag.md itself (its own subject), so the refusal names a.md alone, and the
// command the refusal gives — amending the commit that changed it — grounds it.
func TestT041_40_AWhenReadingTheWholeChangesetAsContext(t *testing.T) {
	const guard = `match: "memories/**"
require:
  - citation: {source_types: [user]}
    when: ./when-flagged.sh
`
	const whenFlagged = `#!/usr/bin/env bash
set -uo pipefail
p="$(cat)"
subject="$(printf '%s' "$p" | jq -r '.subject.files[0]')"
[ "$(printf '%s' "$p" | jq -r '.subject.files | length')" = 1 ] || exit 0
[ "$subject" = "memories/flag.md" ] && exit 1
printf '%s' "$p" | jq -e '[.changeset.files[].path] | index("memories/flag.md")' >/dev/null && exit 0
exit 1
`
	const ask = "adopt a decision log"
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "grounded-memories", guard, map[string]string{"when-flagged.sh": whenFlagged})
	e.CommitAll(proj, "baseline")

	// Without the flag in the changeset the requirement applies to nothing: a.md alone passes.
	e.Run(proj, "s-041-40a", ask, Turns("done",
		Write("w1", "memories/a.md", "# a\n"),
	).ThenCommit("write a"))
	if blocks := stopRefusal(e, proj, "s-041-40a"); blocks != "" {
		t.Fatalf("a changeset the `when` waives for every file was refused: %s", blocks)
	}

	// With it, a.md is asked and flag.md is not.
	const sess = "s-041-40b"
	e.Run(proj, sess, ask, Turns("done",
		Write("w1", "memories/a.md", "# a changed\n"),
		Write("w2", "memories/flag.md", "# flag\n"),
	).ThenCommit("write a and the flag"))
	refusal := stopRefusal(e, proj, sess)
	if got := harness.UngroundedFiles(refusal); got != "memories/a.md" {
		t.Fatalf("the refusal names %q as not grounded, want only memories/a.md:\n%s", got, refusal)
	}
	if !strings.Contains(refusal, "an empty commit carrying only the trailer does not count") {
		t.Errorf("the refusal does not say an empty trailer-only commit grounds nothing:\n%s", refusal)
	}
	refused := len(e.StopContinuations(proj, sess))

	// Their one commit is HEAD: the refusal's command is an amend of it.
	e.Run(proj, sess, "go on", Turns("done",
		harness.RefusalCommand(t, "fix", refusal, "git commit --amend", ask),
	))
	if got := len(e.StopContinuations(proj, sess)); got != refused {
		t.Fatalf("the refusal's own command did not ground the file (%d refusals, had %d):\n%s", got, refused, stopRefusal(e, proj, sess))
	}
}
