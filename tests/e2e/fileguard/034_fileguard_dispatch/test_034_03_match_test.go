package e2e

import (
	"testing"
)

// This file covers a file-guard's MATCH — that it judges only the files its match
// selects, by path AND by marker. A guard fires on a file's STATE, so a file
// outside its match is untouched however not-fine its content, and a file the
// match selects by a marker is judged wherever it sits.

// T034_07: a guard does not fire on a file its path match does not select.
//
// The control for the match: the guard would refuse this content if it saw it,
// but the file is outside memories/, so the guard is never asked and the turn is
// not blocked.
func TestT034_07_GuardDoesNotFireOutsidePathMatch(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "no-secrets", forbidSecretGuard, map[string]string{"check.sh": checkForbidSecret})
	commitGuards(t, proj) // keep the guard's own check.sh out of the cycle diff

	// A file OUTSIDE memories/ holding the very content the guard refuses.
	e.Run(proj, "s-034-07", "write a secret outside the guarded area", Turns("done",
		Write("w1", "docs/readme.md", "holds a SECRET"),
	))

	if len(e.BlockingErrorsFrom(proj, "s-034-07", "Stop")) != 0 {
		t.Errorf("a file-guard matched on memories/ fired on a file under docs/")
	}
	if n := fileGuardLedger(t, proj, "no-secrets"); n != 0 {
		t.Errorf("the guard ran on a file its match did not select (%d times)", n)
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
// marker) whose content still holds TODO.
const checkInvariantDone = `#!/bin/sh
payload="$(cat)"
if printf '%s' "$payload" | grep -q 'TODO'; then
  echo '{"reason":"a file carrying an sr:invariant marker must not be left with a TODO"}'
  exit 1
fi
exit 0
`

// T034_08: a marker-matched guard fires on a file carrying the marker, wherever
// it sits, and NOT on one without it.
//
// The marker line is `// sr:invariant User.id` — a well-formed marker (kind
// `invariant`, fqn `User.id`) the scanner reads off the settled file. The
// not-fine condition (a TODO) is separate from the marker, so the MATCH is what
// selects the file.
func TestT034_08_GuardMatchesByMarker(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "invariant-done", markerGuard, map[string]string{"check.sh": checkInvariantDone})
	commitGuards(t, proj) // keep the guard's own check.sh out of the cycle diff

	// A file carrying an sr:invariant marker AND a TODO — selected by the match,
	// refused by the check.
	e.Run(proj, "s-034-08", "write code with an unfinished invariant", Turns("done",
		Write("w1", "src/model.go", "// sr:invariant User.id\n// TODO enforce it\npackage model"),
	))
	if len(e.BlockingErrorsFrom(proj, "s-034-08", "Stop")) == 0 {
		t.Errorf("a marker-matched guard did not fire on a file carrying the marker")
	}

	// A DIFFERENT session: a file with NO marker is not selected, even holding a
	// TODO.
	e2 := New(t)
	proj2 := e2.Project()
	e2.GitInit(proj2)
	e2.FileGuard(proj2, "invariant-done", markerGuard, map[string]string{"check.sh": checkInvariantDone})
	commitGuards(t, proj2) // keep the guard's own check.sh out of the cycle diff
	e2.Run(proj2, "s-034-08b", "write ordinary code with a TODO", Turns("done",
		Write("w1", "src/other.go", "// TODO later\npackage other"),
	))
	if len(e2.BlockingErrorsFrom(proj2, "s-034-08b", "Stop")) != 0 {
		t.Errorf("a marker-matched guard fired on a file that carries no marker")
	}
}
