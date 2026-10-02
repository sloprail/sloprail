package e2e

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// changeset_command: `sr-session changeset --rule <name>` — what a file-guard
// would be judged on, printed without running any check. These tests drive the
// COMPILED binary against a sandboxed git repository, so they prove the engine's
// range and payload logic (the three floors, the squashed diff, statuses and
// deletions, match scope, trailers and their citations) through the one entry
// point that shows them.
//
// The environment is blanked of a session id unless a test means to have one:
// an ambient CLAUDE_CODE_SESSION_ID would resolve the developer's own session
// and change the answer.

type Env = harness.Env

var (
	New       = harness.New
	Turns     = harness.Turns
	Bash      = harness.Bash
	CitesUser = harness.CitesUser
	CitesTool = harness.CitesTool
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// Shown is what `sr-checks changeset` prints.
type Shown struct {
	Rule                string   `json:"rule"`
	Base                string   `json:"base"`
	Head                string   `json:"head"`
	RuleHash            string   `json:"ruleHash"`
	UnresolvedCitations []string `json:"unresolvedCitations"`
	Payload             struct {
		Event     struct{ Kind string } `json:"event"`
		Changeset struct {
			Base    string `json:"base"`
			Head    string `json:"head"`
			Commits []struct {
				SHA      string              `json:"sha"`
				Subject  string              `json:"subject"`
				Trailers map[string][]string `json:"trailers"`
			} `json:"commits"`
			Files []struct {
				Path       string `json:"path"`
				Status     string `json:"status"`
				OldPath    string `json:"oldPath"`
				OldContent string `json:"oldContent"`
				NewContent string `json:"newContent"`
				Diff       string `json:"diff"`
			} `json:"files"`
			Others []struct {
				Path   string `json:"path"`
				Status string `json:"status"`
			} `json:"others"`
			Citations []struct {
				Quote       string   `json:"quote"`
				SourceTypes []string `json:"sourceTypes"`
				Message     string   `json:"message"`
			} `json:"citations"`
		} `json:"changeset"`
		Subject struct {
			ID    string   `json:"id"`
			Files []string `json:"files"`
		} `json:"subject"`
	} `json:"payload"`
}

// show runs `sr-checks changeset --rule <rule> --base <base> --head HEAD` in proj and returns the parsed
// output (an empty base leaves --base off). The process's exit code and output are returned too; on a non-zero
// exit the parsed value is zero.
func show(t *testing.T, e *harness.Env, proj string, env []string, rule, base string) (Shown, harness.Result) {
	t.Helper()
	args := []string{"changeset", "--rule", rule, "--head", "HEAD"}
	if base != "" {
		args = append(args, "--base", base)
	}
	res := e.CLIDirectEnv(proj, env, "sr-checks", args...)
	var s Shown
	if res.Code == 0 {
		if err := json.Unmarshal([]byte(res.Output), &s); err != nil {
			t.Fatalf("changeset printed unreadable JSON: %v\n%s", err, res.Output)
		}
	}
	return s, res
}

// passingCheck is a check that permits everything. The command must never run it.
const passingCheck = `#!/bin/sh
cat >/dev/null
exit 0
`

// recordingCheck is passingCheck that also records that it ran, into led (a harness
// ledger, outside the project), so a test can say the command ran nothing.
func recordingCheck(led *harness.Ledger) string {
	return "#!/bin/sh\ncat >/dev/null\necho ran >> " + led.Sh() + "\nexit 0\n"
}

// docsRule is a file-guard over docs/ (with optional extra yaml lines).
func docsRule(extra string) string {
	return "match: \"docs/**\"\n" + extra + "checks:\n  - script: ./check.sh\n"
}

// repoWithRule is a committed repository holding docs/a.md and a committed rule
// "size", returning the environment, the project, the commit BEFORE the rule's commit (its floor: the rule's own commit is judged too), and the ledger the rule's check records into.
func repoWithRule(t *testing.T, ruleYAML string) (*harness.Env, string, string, *harness.Ledger) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/a.md", "one\n")
	e.WriteFile(proj, "README.md", "readme\n")
	floor := e.CommitAll(proj, "the project before the rule")
	led := e.NewLedger("ledger")
	e.FileGuard(proj, "size", ruleYAML, map[string]string{"check.sh": recordingCheck(led)})
	e.CommitAll(proj, "add the size rule")
	return e, proj, floor, led
}
