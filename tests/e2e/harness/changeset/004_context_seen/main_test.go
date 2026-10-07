package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

type Env = harness.Env

var (
	New   = harness.New
	Turns = harness.Turns
	Write = harness.Write
	Say   = harness.Say
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// T004_01: retiring the per-file Post events for file-guards leaves the Post file
// events a CONTEXT binds exactly as they were, `seen` flag included. The context
// here plays research-rigor's part: it records, for every PostFileWrite it is
// handed, whether the file was already shown to an earlier Stop
// (`.event.seen`, which research-rigor's enter.sh reads so that an uncommitted
// NOTES.md does not reopen a run at every Stop). A file-guard is declared beside
// it, to show its presence changes nothing for the context.
func TestT004_01_ContextsKeepPostFileEventsAndSeen(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	led := filepath.Join(t.TempDir(), "ledger")
	e.Context(proj, "watch", "on:\n  - event: PostFileWrite\n    match: event.path startsWith \"notes/\"\nenter: ./enter.sh\nexit: ./exit.sh\n", map[string]string{
		"enter.sh": "#!/bin/sh\ninput=\"$(cat)\"\nprintf '%s' \"$input\" | jq -r '[.event.kind, .event.path, (.event.seen // false | tostring)] | join(\" \")' >> " + led + "\nprintf '{}'\n",
		"exit.sh":  "#!/bin/sh\ncat >/dev/null\nexit 1\n",
	})
	e.FileGuard(proj, "docs", "match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n", map[string]string{"check.sh": "#!/bin/sh\ncat >/dev/null\nexit 0\n"})
	e.CommitAll(proj, "the project")

	lines := func() []string {
		b, _ := os.ReadFile(led)
		return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(b)), "\n", " \n "))
	}

	// Cycle 1: an uncommitted, unguarded note. The context is handed it: not seen.
	e.Run(proj, "s-004-01", "take a note", Turns("done", Write("w1", "notes/a.md", "a note\n")))
	got := strings.Join(lines(), " ")
	if !strings.Contains(got, "PostFileCreate notes/a.md false") {
		t.Fatalf("the context was not handed the Post file event on a guarded-project's first Stop:\n%s", got)
	}

	// Cycle 2: the file is unchanged and still uncommitted, so it is in the
	// difference again — and the context is told it was already seen.
	e.Run(proj, "s-004-01", "anything else?", Turns("done", Say("m1", "No.")))
	got = strings.Join(lines(), " ")
	if !strings.Contains(got, "notes/a.md true") {
		t.Fatalf("a file an earlier Stop already delivered was not marked seen:\n%s", got)
	}
}
