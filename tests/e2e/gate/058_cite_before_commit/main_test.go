package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The plugin's sloprail/gate/cite-before-commit (on by default) refuses a `git commit` that would
// change a file a file-guard guards with `require: citation`, unless the command carries a cite:
// the prevention half of what Stop and CI's `sr-checks verify` refuse afterwards.

type Env = harness.Env

var (
	Turns = harness.Turns
	Bash  = harness.Bash
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// prompt is the user's own message every scenario cites.
const prompt = "record the decision to adopt a decision log"

const quote = "adopt a decision log"

const citedDocs = "match: \"docs/**\"\nrequire:\n  - citation: {source_types: [user]}\nchecks:\n  - script: ./ok.sh\n"

func project(t *testing.T) (*Env, string) {
	t.Helper()
	e := harness.New(t, harness.WithoutShippedFileGuards(), harness.WithCiteBeforeCommit())
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.WriteFile(proj, "src/seed.go", "package seed\n")
	e.CommitAll(proj, "the project")
	e.FileGuard(proj, "cited-docs", citedDocs, map[string]string{"ok.sh": "#!/bin/sh\nexit 0\n"})
	e.CommitAll(proj, "the rule")
	return e, proj
}

func stage(id, path, body string) harness.Turn {
	return Bash(id, "mkdir -p \"$(dirname "+path+")\" && printf '%s' '"+body+"' > "+path+" && git add "+path)
}

func citedCommit(subject string) string {
	return "sr-session trajectory cite '" + quote + "' && git commit -q -m '" + subject + "' -m 'Sloprail-Cites-User: " + quote + "'"
}

func subjects(e *Env, proj string) string { return e.Git(proj, "log", "--format=%s") }

func has(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("missing %q in:\n%s", want, got)
	}
}
