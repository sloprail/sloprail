package e2e

import (
	"strings"
	"testing"
)

// refuseMoved is a check that refuses whenever the changeset holds a rename: what a
// `deletions: include` rule over memories/ says about a guarded file moved away.
const refuseMoved = `#!/bin/sh
payload="$(cat)"
if printf '%s' "$payload" | jq -e 'any(.changeset.files[]; .status == "R")' >/dev/null; then
  echo '{"reason":"a guarded file was moved away"}'
  exit 1
fi
exit 0
`

const memoriesRule = "match: \"memories/**\"\ndeletions: include\nchecks:\n  - script: ./check.sh\n"

// movedProject is a committed repository with memories/x.md, notes/n.md and the rule
// committed after them (so the seed is the baseline, not part of the range).
func movedProject(t *testing.T) (*Env, string) {
	t.Helper()
	e := NewUncited(t)
	proj := e.Project()
	e.GitInit(proj)
	body := "a memory long enough to be seen as a rename\nsecond line\nthird line\n"
	e.WriteFile(proj, "memories/x.md", body)
	e.WriteFile(proj, "notes/n.md", body)
	e.FileGuard(proj, "memories", memoriesRule, map[string]string{"check.sh": refuseMoved})
	e.CommitSeedThenRules(proj, "the project")
	return e, proj
}

// T003_24: a rename OUT of a guarded path is judged by the rule that guards the old
// path. `git mv memories/x.md archive/x.md` leaves nothing at memories/, and a rule
// that only asked about the new path would never see the loss of the file.
// sr:proves fileguard/rename-selected-by-either-path
func TestT003_24_ARenameOutOfAGuardedPathIsJudged(t *testing.T) {
	e, proj := movedProject(t)
	e.Run(proj, "s-003-24", "archive the memory", Turns("done",
		Bash("b1", "mkdir -p archive && git mv memories/x.md archive/x.md && git commit -q -m 'archive the memory'"),
	))
	blocks := strings.Join(e.BlockingErrorsFrom(proj, "s-003-24", "Stop"), "\n")
	if !strings.Contains(blocks, "a guarded file was moved away") {
		t.Fatalf("a rename out of a guarded path was not judged and refused; blocking errors: %q", blocks)
	}

	// Control: the same move between two unguarded paths is nobody's business.
	e2, proj2 := movedProject(t)
	e2.Run(proj2, "s-003-24b", "rename the note", Turns("done",
		Bash("b1", "git mv notes/n.md notes/m.md && git commit -q -m 'rename the note'"),
	))
	if errs := e2.BlockingErrorsFrom(proj2, "s-003-24b", "Stop"); len(errs) != 0 {
		t.Fatalf("a rename between unguarded paths was refused: %q", errs)
	}
}
