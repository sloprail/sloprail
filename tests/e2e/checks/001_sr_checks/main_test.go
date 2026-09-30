package e2e

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// sr_checks: `sr-checks status` and `sr-checks sql` — reading what a session's
// file-guards concluded about its commits. Driven through the COMPILED binary
// against a check-results database seeded through the store (the writer is the
// Stop evaluation; what is under test here is the reader, which must only read).

type Env = harness.Env

var (
	New   = harness.New
	Turns = harness.Turns
	Bash  = harness.Bash
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

const sessionID = "s-checks-001"

// session is a project with a session the mock has run, so the engine knows its
// identity and where its check results belong.
func session(t *testing.T) (*Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Run(proj, sessionID, "hello", Turns("done", Bash("b1", "true")))
	return e, proj
}

func inSession(e *Env) []string {
	return []string{"CLAUDE_CODE_SESSION_ID=" + sessionID, "CLAUDE_CONFIG_DIR=" + e.ConfigDir(), "CLAUDECODE="}
}

// checks runs `sr-checks <args>` in the session.
func checks(e *Env, proj string, args ...string) harness.Result {
	return e.CLIDirectEnv(proj, inSession(e), "sr-checks", args...)
}

// Row is one `sr-checks status --json` row.
type Row struct {
	Rule    string `json:"rule"`
	Subject string `json:"subject"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	BaseRef string `json:"base_ref"`
	HeadRef string `json:"head_ref"`
	Error   string `json:"error"`
}

func statusRows(t *testing.T, e *Env, proj string, args ...string) []Row {
	t.Helper()
	res := checks(e, proj, append([]string{"status", "--json"}, args...)...)
	if res.Code != 0 {
		t.Fatalf("status exited %d:\n%s", res.Code, res.Output)
	}
	var rows []Row
	if err := json.Unmarshal([]byte(res.Output), &rows); err != nil {
		t.Fatalf("status printed unreadable JSON: %v\n%s", err, res.Output)
	}
	return rows
}

func run(rule, head string) checkstore.CheckRun {
	return checkstore.CheckRun{CheckID: rule, BaseRef: "base000", HeadRef: head,
		Metadata: map[string]any{"ruleHash": "h1", "eventKind": "Changeset", "baseOrigin": "floor"}}
}

func judge(status, fp, why string) checkstore.CheckRecord {
	return checkstore.CheckRecord{Subject: "changeset", Kind: "check[1]:judge:./rubric.md.j2", Status: status,
		Fingerprint: fp, Metadata: map[string]any{"reasoning": why, "model": "size-md"}}
}

func script(status string) checkstore.CheckRecord {
	return checkstore.CheckRecord{Subject: "changeset", Kind: "check[0]:script:./size.sh", Status: status}
}

func contains(t *testing.T, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Fatalf("output does not contain %q:\n%s", w, out)
		}
	}
}
