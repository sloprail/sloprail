package e2e

import (
	"strings"
	"testing"
)

// T038_07: an unknown `deletions:` value is REFUSED when the rule loads, named
// where an author looks — `sr-file declarations` and `sr-session start` — and is
// never read as the default. A typo that silently became `skip` would switch off
// exactly the deletions the author wrote the key to catch.
//
// On the old engine the key was an unknown field, tolerated and ignored, so the
// guard loaded clean.
func TestT038_07_UnknownDeletionsValueIsRefusedAtLoad(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "typo", "match: \"docs/**\"\ndeletions: inlcude\nchecks:\n  - script: ./check.sh\n",
		map[string]string{"check.sh": "#!/bin/sh\ncat >/dev/null\nexit 0\n"})
	e.FileGuard(proj, "fine", guardYAML("only"),
		map[string]string{"check.sh": "#!/bin/sh\ncat >/dev/null\nexit 0\n"})

	decl := e.CLIDirect(proj, "sr-file", "declarations", proj)
	if decl.Code != 1 {
		t.Fatalf("sr-file declarations should exit 1 on an invalid declaration, got %d:\n%s", decl.Code, decl.Output)
	}
	for _, want := range []string{"file-guard/typo", "deletions", "inlcude", "skip, include, only"} {
		if !strings.Contains(decl.Output, want) {
			t.Errorf("sr-file declarations did not report %q:\n%s", want, decl.Output)
		}
	}
	if !strings.Contains(decl.Output, "fine") || strings.Contains(decl.Output, "file-guard/fine") {
		t.Errorf("the sound guard with a valid deletions value must still load:\n%s", decl.Output)
	}

	start := e.CLI(proj, "session", "start")
	if !strings.Contains(start.Output, "not loaded") || !strings.Contains(start.Output, "inlcude") {
		t.Errorf("sr-session start did not report the refused deletions value:\n%s", start.Output)
	}
}
