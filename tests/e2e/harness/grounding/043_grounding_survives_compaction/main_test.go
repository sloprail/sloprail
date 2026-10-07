package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// What a session grounded before its context was compacted is still grounded after it,
// and the session is the same session: its identity and its state do not reset.
//
// These say nothing of any one harness: the steps are the neutral ones (Write, Bash,
// Compact), and what is asserted is sloprail's own — whether a quote resolves, whether
// a commit is let through or refused, what a rule stored — so the package runs on
// whichever harness SR_HARNESS names. A harness whose compaction makes sloprail lose
// the record, mint a new identity or see a quote twice fails here, which is the point.

var (
	Turns   = harness.Turns
	Bash    = harness.Bash
	Write   = harness.Write
	Compact = harness.Compact
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// prompt is the user's own message every scenario cites.
const prompt = "record the decision to adopt a decision log"

// userQuote is in the prompt; toolQuote is what a command prints.
const (
	userQuote = "adopt a decision log"
	toolQuote = "BUILD-GREEN-4471 all checks passed"
)

// noCitation is the refusal's own words for a range no citation grounds.
const noCitation = "in the commit that last changed it, and that commit carries none that resolves"

const citedDocs = "match: \"docs/**\"\nrequire:\n  - citation: {source_types: [user]}\nchecks:\n  - script: ./ok.sh\n"

const citedTools = "match: \"results/**\"\nrequire:\n  - citation: {source_types: [tool_result]}\nchecks:\n  - script: ./ok.sh\n"

// project is a git repository whose docs/ need the user's words and whose results/ need a
// tool's output, with the plugin's cite-before-commit gate in force.
func project(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e := harness.New(t, harness.WithoutShippedFileGuards())
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.WriteFile(proj, "results/seed.md", "seed\n")
	e.CommitAll(proj, "the project")
	ok := map[string]string{"ok.sh": "#!/bin/sh\nexit 0\n"}
	e.FileGuard(proj, "cited-docs", citedDocs, ok)
	e.FileGuard(proj, "cited-results", citedTools, ok)
	e.CommitAll(proj, "the rules")
	return e, proj
}

// stage writes a file and stages it: two plain steps, since a mock may model a single command only.
func stage(id, path, body string) []harness.Turn {
	return []harness.Turn{Write(id+"w", path, body), Bash(id+"g", "git add "+path)}
}

// citeUser is a cite of the user's words, then a commit carrying the trailer.
func citeUser(id, subject, quote string) []harness.Turn {
	return []harness.Turn{
		Bash(id+"c", "sr-session trajectory cite '"+quote+"'"),
		Bash(id+"m", "git commit -q -m '"+subject+"' -m 'Sloprail-Cites-User: "+quote+"'"),
	}
}

// citeTool is a cite of a tool's output, then a commit carrying the trailer.
func citeTool(id, subject, quote string) []harness.Turn {
	return []harness.Turn{
		Bash(id+"c", "sr-session trajectory cite --source-types tool_result '"+quote+"'"),
		Bash(id+"m", "git commit -q -m '"+subject+"' -m 'Sloprail-Cites-Tool: "+quote+"'"),
	}
}

// steps flattens turns and groups of turns into one list.
func steps(parts ...any) []harness.Turn {
	var out []harness.Turn
	for _, p := range parts {
		switch v := p.(type) {
		case harness.Turn:
			out = append(out, v)
		case []harness.Turn:
			out = append(out, v...)
		}
	}
	return out
}

func has(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("missing %q in:\n%s", want, got)
	}
}

func refusal(e *harness.Env, proj, sess string) string {
	return strings.Join(e.CheckRun(proj, sess), "\n")
}
