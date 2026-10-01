package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// stop_evaluation: at Stop each file-guard is evaluated once over the changeset
// of commits it has not yet passed, and every run is recorded in the session's
// check results. Driven through the mock: the agent commits (or does not), the
// Stop hook evaluates, and the refusal reaches the agent as a blocking error.

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

// recorder is a file-guard check that writes what it is handed to a ledger
// OUTSIDE the project — a check writing into its own folder would change the
// rule's hash on every run — and refuses when a changed file contains FORBIDDEN.
func recorder(ledger string) string {
	return `#!/bin/sh
payload="$(cat)"
printf '%s' "$payload" | jq -c --arg tree "$SR_TREE" --arg base "$SR_BASE" --arg head "$SR_HEAD" '{
  kind: .event.kind, base: .changeset.base, head: .changeset.head,
  files: [.changeset.files[] | {path, status}],
  commits: [.changeset.commits[].subject],
  citations: [.changeset.citations[].quote],
  env_base: $base, env_head: $head, tree_set: ($tree != "")
}' >> ` + ledger + `
if printf '%s' "$payload" | jq -e 'any(.changeset.files[]; .newContent | contains("FORBIDDEN"))' >/dev/null; then
  echo '{"reason":"FORBIDDEN text in the changeset"}'
  exit 1
fi
exit 0
`
}

const docsRule = "match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n"

// Run is one ledger line: what the check was handed.
type Run struct {
	Kind      string   `json:"kind"`
	Base      string   `json:"base"`
	Head      string   `json:"head"`
	Files     []File   `json:"files"`
	Commits   []string `json:"commits"`
	Citations []string `json:"citations"`
	EnvBase   string   `json:"env_base"`
	EnvHead   string   `json:"env_head"`
	TreeSet   bool     `json:"tree_set"`
}

type File struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

func paths(fs []File) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Path)
	}
	return out
}

func ledger(t *testing.T, path string) []Run {
	t.Helper()
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	var runs []Run
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line == "" {
			continue
		}
		var r Run
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("unreadable ledger line %q: %v", line, err)
		}
		runs = append(runs, r)
	}
	return runs
}

// project is a committed repository with a committed "docs" rule recording to a
// ledger outside the tree. Returns the env, the project, the ledger path.
func project(t *testing.T, ruleYAML string) (*Env, string, string) {
	return seededProject(t, ruleYAML, map[string]string{"docs/seed.md": "seed\n", "notes/scratch.md": "scratch\n"}, recorder)
}

// seededProject is project with the files the tree holds before the rule arrives
// given by the test, for a scenario that needs its own starting content, and the
// check's script given from the ledger path it records to (recorder is the usual one).
func seededProject(t *testing.T, ruleYAML string, seed map[string]string, check func(ledger string) string) (*Env, string, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	for path, content := range seed {
		e.WriteFile(proj, path, content)
	}
	led := filepath.Join(t.TempDir(), "ledger.jsonl")
	e.CommitAll(proj, "the project")
	e.FileGuard(proj, "docs", ruleYAML, map[string]string{"check.sh": check(led)})
	e.CommitAll(proj, "the rule")
	return e, proj, led
}

// New is harness.New with the plugin's authoring file-guards switched off: this package
// is about other rules, and the authoring guards would judge the rules' own files.
func New(t *testing.T) *Env { return harness.New(t, harness.WithoutShippedFileGuards()) }

// stopBlocks is how many times a session's Stop has refused and been gone past so
// far. Every refusal counts, the same words or not (BlockingErrors de-duplicates).
func stopBlocks(e *Env, proj, sess string) int {
	return len(e.StopContinuations(proj, sess))
}

// newBlocks is what a session's Stop refused with since `seen` refusals ago.
func newBlocks(e *Env, proj, sess string, seen int) string {
	return strings.Join(e.StopContinuations(proj, sess)[seen:], "\n")
}

// lastRun is the newest ledger line: the most recent thing a check was handed.
func lastRun(t *testing.T, path string) Run {
	t.Helper()
	runs := ledger(t, path)
	if len(runs) == 0 {
		t.Fatal("the check never ran")
	}
	return runs[len(runs)-1]
}
