package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// commit_required: at Stop, an uncommitted change to a path some file-guard's
// match selects refuses the turn — "commit these". Always on, never commits for
// the agent, in the harness's real blocking form, only for an agent that owns the
// tree, with a loop breaker in the spirit of stop_hook_block_cap.
//
// Run as `env -u CLAUDECODE -u CLAUDE_CODE_SESSION_ID go test ...`: an ambient
// session id leaks into the mock.

type Env = harness.Env

var (
	New      = harness.New
	Turns    = harness.Turns
	Bash     = harness.Bash
	Write    = harness.Write
	Dispatch = harness.Dispatch
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// rule is a file-guard over docs/ whose check always passes and never matters:
// commit-required is about what the rule SELECTS, not what its check thinks.
const rule = "match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n"

const passing = "#!/bin/sh\ncat >/dev/null\nexit 0\n"

// project is a committed repository with docs/seed.md and a committed rule.
func project(t *testing.T) (*Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.WriteFile(proj, "notes/scratch.md", "scratch\n")
	e.FileGuard(proj, "docs", rule, map[string]string{"check.sh": passing})
	e.CommitAll(proj, "the project and its rule")
	return e, proj
}

func subScenario(t *testing.T, s harness.Scenario) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sub.sh")
	if err := s.Script(path); err != nil {
		t.Fatalf("write sub-agent scenario: %v", err)
	}
	return path
}

// commitRequired counts the Stop refusals in the session's record that are this
// gate's, by its own words.
func commitRequired(errs []string) []string {
	var out []string
	for _, e := range errs {
		if strings.Contains(e, "Commit your work before ending this turn") {
			out = append(out, e)
		}
	}
	return out
}

// stopPayload is the hook payload a harness sends at Stop.
func stopPayload(e *Env, proj, sessionID string, active bool) string {
	b, _ := json.Marshal(map[string]any{
		"session_id":       sessionID,
		"transcript_path":  e.TranscriptPath(proj, sessionID),
		"cwd":              proj,
		"stop_hook_active": active,
		"hook_event_name":  "Stop",
	})
	return string(b)
}

// stop runs `sr-session stop` the way the harness does and returns its result.
func stop(e *Env, proj, sessionID string, active bool) harness.Result {
	return e.CLIDirectStdinEnv(proj, stopPayload(e, proj, sessionID, active),
		[]string{"CLAUDE_CONFIG_DIR=" + e.ConfigDir(), "CLAUDECODE=", "CLAUDE_CODE_SESSION_ID="}, "sr-session", "stop")
}

func blocked(r harness.Result) bool { return strings.Contains(r.Output, `"decision":"block"`) }
