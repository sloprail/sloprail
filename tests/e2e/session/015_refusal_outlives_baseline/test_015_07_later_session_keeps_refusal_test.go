package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/session/changesetkit"
)

// T015_07: a rule that existed at session start is judged over the range the folder
// tracks (the merge base with origin's default branch up to HEAD), which a later session
// of the same worktree shares: what an earlier session was refused for and never fixed
// is still in it. Session one is refused on x.md; session two starts later and is still
// refused on it (the refusal does not vanish with the new session); fixing it passes;
// session three then starts and is not refused.
//
// What the per-session extension did on top — session three not being handed x.md's old
// commit again once it had passed — is gone with it: the range is whatever the branch
// holds ahead of the default branch, and the checks run over all of it every time, so
// x.md is put to the rule again (now holding clean content) and passes.
func TestT015_07_AnEarlierSessionsUnfixedRefusalStaysInSightUntilFixed(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	seen := e.NewLedger("seen")
	script := `#!/bin/sh
payload="$(cat)"
paths="$(printf '%s' "$payload" | jq -r '.changeset.files[].path')"
[ -n "$paths" ] || exit 0
root="${SR_GUARDRAIL_DIR%/.sloprail/file-guard/*}"
printf '%s\n' "$payload" >> ` + seen.Sh() + `
for path in $paths; do
  if [ -f "$root/$path" ] && grep -q FORBIDDEN "$root/$path"; then
    echo '{"reason":"still contains the forbidden word"}'; exit 1
  fi
done
exit 0
`
	e.FileGuard(proj, "watcher", "match: \"**/*.md\"\nchecks:\n  - script: ./judge.sh\n", map[string]string{"judge.sh": script})
	e.CommitAll(proj, "the guardrail before the sessions")

	// Session one is refused on x.md and leaves it unfixed.
	e.Run(proj, "s-015-07-one", "write a bad file", Turns("done",
		Write("w1", "x.md", "FORBIDDEN content\n"),
	).ThenCommit("the bad file"))
	if len(e.StopContinuations(proj, "s-015-07-one")) == 0 {
		t.Fatal("premise: session one's bad file was not refused")
	}

	// Session two starts later (after x.md's commit) and is still refused on it.
	first := changesetkit.Files(t, seen.Lines())
	e.Run(proj, "s-015-07-two", "write something else", Turns("done",
		Write("w2", "y.md", "fine\n"),
	).ThenCommit("unrelated work"))
	second := changesetkit.Files(t, seen.Lines())[len(first):]
	if !changesetkit.Saw(second, "x.md") || !changesetkit.Saw(second, "y.md") {
		t.Fatalf("session two was not handed both the refused file and its own work: %v", second)
	}
	if len(e.StopContinuations(proj, "s-015-07-two")) == 0 {
		t.Fatal("session two was not refused for the file session one never fixed")
	}

	// Fixed, session two passes.
	blocks := len(e.StopContinuations(proj, "s-015-07-two"))
	e.Run(proj, "s-015-07-two", "fix it", Turns("done",
		Write("w3", "x.md", "clean content\n"),
	).ThenCommit("fix the bad file"))
	if n := len(e.StopContinuations(proj, "s-015-07-two")); n != blocks {
		t.Fatalf("the fixed range was still refused (%d refusals, had %d)", n, blocks)
	}

	// Session three begins after the fix: the range still holds x.md, now clean, and passes.
	before := len(changesetkit.Files(t, seen.Lines()))
	e.Run(proj, "s-015-07-three", "write another file", Turns("done",
		Write("w4", "z.md", "fine\n"),
	).ThenCommit("more work"))
	third := changesetkit.Files(t, seen.Lines())[before:]
	if len(third) == 0 || !changesetkit.Saw(third, "z.md") {
		t.Fatalf("session three never judged its own work: %v", third)
	}
	if n := len(e.StopContinuations(proj, "s-015-07-three")); n != 0 {
		t.Fatalf("session three was refused (%d)", n)
	}
}
