package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// This file covers a file-guard's MATCH — that it judges only the files its match
// selects, by path AND by marker. A guard fires on a file's STATE, so a file
// outside its match is untouched however not-fine its content, and a file the
// match selects by a marker is judged wherever it sits.
//
// Every negative here is a COMMITTED file the guard would refuse if it saw it, beside
// a committed file the match does select: the guard is asked (the ledger proves it)
// and what it is asked about is exactly the selected file. An uncommitted file would
// only prove the commit-required refusal, which says nothing about the match.

// T034_07: a guard does not fire on a file its path match does not select.
//
// The control for the match: the guard would refuse this content if it saw it,
// but the file is outside memories/, so the guard is not handed it. The same commit
// holds a clean file under memories/, so the guard DOES run — about that file only.
func TestT034_07_GuardDoesNotFireOutsidePathMatch(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	led := e.NewLedger("ledger")
	e.FileGuard(proj, "no-secrets", forbidSecretGuard, map[string]string{"check.sh": checkForbidSecret(led)})
	e.DisableShippedFileGuards(proj)
	e.CommitAll(proj, "the rule and its scripts")

	// A file OUTSIDE memories/ holding the very content the guard refuses, committed
	// with a file the match selects.
	e.Run(proj, "s-034-07", "write a secret outside the guarded area", Turns("done",
		Write("w1", "docs/readme.md", "holds a SECRET"),
		Write("w2", "memories/ok.md", "a perfectly ordinary note"),
	).ThenCommit("a secret outside the guarded area, and a clean memory"))

	// The positive half: the guard ran, and was handed the memory and only the memory.
	if got := led.Lines(); len(got) == 0 || got[0] != "asked memories/ok.md" {
		t.Fatalf("the guard should have been asked about memories/ok.md alone, was asked: %q", got)
	}
	if n := len(e.BlockingErrorsFrom(proj, "s-034-07", "Stop")); n != 0 {
		t.Errorf("a file-guard matched on memories/ refused over a file under docs/ (%d blocking errors)", n)
	}
}

// markerGuard is a file-guard matched by a MARKER, not a path: any file carrying
// an `sr:invariant` marker is judged, wherever it sits. It reads the markers off
// the settled file's state (the event's newMarkers). A file is not fine if it
// also holds a TODO — an invariant left unfinished.
const markerGuard = `match: any(markers, .kind == "invariant")
checks:
  - script: ./check.sh
`

// checkInvariantDone refuses a file (already selected by carrying an sr:invariant
// marker) whose content still holds TODO, recording the paths it was handed into led.
func checkInvariantDone(led *harness.Ledger) string {
	return `#!/bin/sh
payload="$(cat)"
echo "asked $(printf '%s' "$payload" | jq -r '[.changeset.files[].path] | join(",")')" >> ` + led.Sh() + `
if printf '%s' "$payload" | jq -e 'any(.changeset.files[]; (.newContent // "") | contains("TODO"))' >/dev/null; then
  echo '{"reason":"a file carrying an sr:invariant marker must not be left with a TODO"}'
  exit 1
fi
exit 0
`
}

// markerProject is a project with the marker guard committed, and its ledger.
func markerProject(t *testing.T) (*harness.Env, string, *harness.Ledger) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	led := e.NewLedger("ledger")
	e.FileGuard(proj, "invariant-done", markerGuard, map[string]string{"check.sh": checkInvariantDone(led)})
	e.DisableShippedFileGuards(proj)
	e.CommitAll(proj, "the rule and its scripts")
	return e, proj, led
}

// T034_08: a marker-matched guard fires on a file carrying the marker, wherever
// it sits, and NOT on one without it.
//
// The marker line is `// sr:invariant User.id` — a well-formed marker (kind
// `invariant`, fqn `User.id`) the scanner reads off the settled file. The
// not-fine condition (a TODO) is separate from the marker, so the MATCH is what
// selects the file.
//
// Both halves are committed, so what refuses is the guard's own check and never the
// engine asking for a commit.
func TestT034_08_GuardMatchesByMarker(t *testing.T) {
	e, proj, led := markerProject(t)

	// A file carrying an sr:invariant marker AND a TODO — selected by the match,
	// refused by the check.
	e.Run(proj, "s-034-08", "write code with an unfinished invariant", Turns("done",
		Write("w1", "src/model.go", "// sr:invariant User.id\n// TODO enforce it\npackage model"),
	).ThenCommit("an unfinished invariant"))
	blocks := strings.Join(e.BlockingErrorsFrom(proj, "s-034-08", "Stop"), "\n")
	if !strings.Contains(blocks, "must not be left with a TODO") {
		t.Errorf("a marker-matched guard did not refuse a committed file carrying the marker:\n%s", blocks)
	}
	if got := led.Lines(); len(got) == 0 || got[0] != "asked src/model.go" {
		t.Errorf("the guard should have been asked about src/model.go, was asked: %q", got)
	}
}

// T034_08b: the same guard leaves a committed file WITHOUT the marker alone, even
// holding a TODO — beside a marked, finished file that does select it, so the guard runs.
func TestT034_08b_GuardIgnoresAFileWithoutTheMarker(t *testing.T) {
	e, proj, led := markerProject(t)

	e.Run(proj, "s-034-08b", "write ordinary code with a TODO", Turns("done",
		Write("w1", "src/other.go", "// TODO later\npackage other"),
		Write("w2", "src/done.go", "// sr:invariant User.name\npackage done"),
	).ThenCommit("ordinary code with a TODO, and a finished invariant"))

	if got := led.Lines(); len(got) == 0 || got[0] != "asked src/done.go" {
		t.Fatalf("the guard should have been asked about src/done.go alone, was asked: %q", got)
	}
	if blocks := e.BlockingErrorsFrom(proj, "s-034-08b", "Stop"); len(blocks) != 0 {
		t.Errorf("a marker-matched guard fired on a file that carries no marker: %q; asked %q", blocks, led.Lines())
	}
}
