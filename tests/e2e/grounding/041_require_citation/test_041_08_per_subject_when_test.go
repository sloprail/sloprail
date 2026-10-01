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
// command the refusal gives — a follow-up commit that changes it and carries the quote — grounds it.
// With HEAD pushed the amend is not even offered.
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

	// Their one commit is HEAD, unpushed, with a clean tree: the amend is offered, but the
	// recommended fix — and the command followed here — is a follow-up commit that
	// changes the file and carries the quote.
	if !strings.Contains(refusal, "git commit --amend --no-edit --trailer") {
		t.Errorf("an unpushed HEAD that changed the file should be offered the amend:\n%s", refusal)
	}
	e.Run(proj, sess, "go on", Turns("done",
		Write("w3", "memories/a.md", "# a changed again\n"),
		harness.RefusalCommand(t, "fix", refusal, "git add", ask),
	))
	if got := len(e.StopContinuations(proj, sess)); got != refused {
		t.Fatalf("the refusal's own command did not ground the file (%d refusals, had %d):\n%s", got, refused, stopRefusal(e, proj, sess))
	}
}

// T041_54: when the commit that changed the file is HEAD but HEAD is already pushed (a
// remote branch contains it), rewriting it would diverge from what others have: the
// refusal offers no amend and no squash, only the follow-up commit — which grounds it.
func TestT041_54_APushedHeadIsNeverOfferedAnAmend(t *testing.T) {
	const guard = `match: "memories/**"
require:
  - citation: {source_types: [user]}
`
	const ask = "adopt a decision log"
	const sess = "s-041-54"
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "grounded-memories", guard, nil)
	e.CommitAll(proj, "baseline")

	e.Run(proj, sess, ask, Turns("done",
		Write("w1", "memories/a.md", "# a\n"),
	).ThenCommit("write a"))
	// Premise: unpushed, the amend is on offer.
	if first := stopRefusal(e, proj, sess); !strings.Contains(first, "--amend") {
		t.Fatalf("premise: an unpushed HEAD should be offered the amend:\n%s", first)
	}
	e.Git(proj, "update-ref", "refs/remotes/origin/main", "HEAD")
	e.Run(proj, sess, "go on", Turns("done", Bash("b1", "true")))
	all := e.AllBlockingErrorsFrom(proj, sess, "Stop")
	refusal := all[len(all)-1]
	if got := harness.UngroundedFiles(refusal); got != "memories/a.md" {
		t.Fatalf("the refusal names %q as not grounded, want memories/a.md:\n%s", got, refusal)
	}
	if strings.Contains(refusal, "--amend") || strings.Contains(refusal, "reset --soft") {
		t.Fatalf("a pushed HEAD was offered a history rewrite:\n%s", refusal)
	}
	refused := len(e.StopContinuations(proj, sess))

	e.Run(proj, sess, "go on", Turns("done",
		Write("w2", "memories/a.md", "# a changed\n"),
		harness.RefusalCommand(t, "fix", refusal, "git add", ask),
	))
	if got := len(e.StopContinuations(proj, sess)); got != refused {
		t.Fatalf("the follow-up commit did not ground the file (%d refusals, had %d):\n%s", got, refused, stopRefusal(e, proj, sess))
	}
}
