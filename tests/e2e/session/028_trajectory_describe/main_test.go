package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// trajectory describe — facts ABOUT a trajectory: whether it belongs to a
// sub-agent, which trajectory spawned it, and which it spawned. These tests
// exercise the COMPILED sr-session binary as a subprocess against real-layout
// fixture trajectories, asserting the JSON on stdout.
//
// # Why fixtures rather than the mock
//
// describe and cite do not take part in the hook lifecycle — they are run by a
// hook script or an agent with a trajectory path, not fired by the harness at a
// pause point. So the honest e2e is the one the test-cli-command skill names:
// compile the binary, run it against a fixture, assert stdout. Driving the mock
// would add nothing describe reads — and could not reach the case that matters
// most, the parent correlation: the mock writes a sub-agent's meta file with an
// EMPTY toolUseId (measured), so a mock-driven run can never set parentPath. The
// fixtures here carry the toolUseId Claude Code actually writes, so the full
// correlation runs end to end through the real binary. The mock's own sub-agent
// layout — and that this repo's plugin binds a sub-agent's cycle at all — is
// covered in tests/e2e/subagent.
//
// The fixture shapes are taken verbatim from real ~/.claude transcripts: a root
// with a parentless origin, an assistant turn dispatching a sub-agent by a
// tool_use id, and a <session>/subagents/agent-<id>.jsonl record with an
// agent-<id>.meta.json companion naming that same id.

type Env = harness.Env

var New = harness.New

// TestMain removes the binary build dir when this package's tests finish.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// traj is one conversation's on-disk layout under a project directory: a root
// transcript, and any sub-agent records nested beside it the way Claude Code
// nests them.
type traj struct {
	t   *testing.T
	dir string // the project directory holding <session>.jsonl and <session>/
}

// newTraj makes an empty project directory for a conversation's transcripts.
func newTraj(t *testing.T) *traj {
	t.Helper()
	// A short root: the identity walk never runs here (describe is handed the
	// path), but keeping it shallow matches the other fixtures and avoids any
	// path-length surprise.
	dir, err := os.MkdirTemp("", "traj-")
	if err != nil {
		t.Fatalf("temp project: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return &traj{t: t, dir: dir}
}

// root writes a root transcript at <dir>/<session>.jsonl and returns its path.
func (tr *traj) root(session string, lines ...string) string {
	tr.t.Helper()
	return writeLines(tr.t, filepath.Join(tr.dir, session+".jsonl"), lines...)
}

// subagent writes a sub-agent record at <dir>/<session>/subagents/agent-<id>.jsonl
// and returns its path.
func (tr *traj) subagent(session, agentID string, lines ...string) string {
	tr.t.Helper()
	dir := filepath.Join(tr.dir, session, "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		tr.t.Fatalf("mkdir subagents: %v", err)
	}
	return writeLines(tr.t, filepath.Join(dir, "agent-"+agentID+".jsonl"), lines...)
}

// meta writes the companion meta file beside a sub-agent's record.
func (tr *traj) meta(session, agentID string, m map[string]any) {
	tr.t.Helper()
	dir := filepath.Join(tr.dir, session, "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		tr.t.Fatalf("mkdir subagents: %v", err)
	}
	b, err := json.Marshal(m)
	if err != nil {
		tr.t.Fatalf("marshal meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-"+agentID+".meta.json"), b, 0o644); err != nil {
		tr.t.Fatalf("write meta: %v", err)
	}
}

func writeLines(t *testing.T, path string, lines ...string) string {
	t.Helper()
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// --- fixture record shapes, taken from real transcripts ---

func rootLine(uuid string) string {
	return `{"type":"user","uuid":"` + uuid + `","parentUuid":null,"isSidechain":false,` +
		`"timestamp":"2026-08-13T09:00:00.000Z","message":{"role":"user","content":"start the work"}}`
}

// dispatchLine is an assistant turn dispatching a sub-agent by a tool_use id —
// the id a sub-agent's meta file points back at.
func dispatchLine(uuid, parent, toolUseID string) string {
	return `{"type":"assistant","uuid":"` + uuid + `","parentUuid":"` + parent + `","isSidechain":false,` +
		`"timestamp":"2026-08-13T09:02:00.000Z","message":{"role":"assistant","content":[` +
		`{"type":"text","text":"delegating"},` +
		`{"type":"tool_use","id":"` + toolUseID + `","name":"Agent","input":{"description":"go"}}]}}`
}

// sidechainRootLine is a sub-agent transcript's own parentless origin, carrying
// isSidechain the way every real sub-agent record does.
func sidechainRootLine(uuid, agentID string) string {
	return `{"type":"user","uuid":"` + uuid + `","parentUuid":null,"isSidechain":true,` +
		`"agentId":"` + agentID + `","timestamp":"2026-08-13T09:03:00.000Z",` +
		`"message":{"role":"user","content":"do the delegated thing"}}`
}

// decodeDescribe parses the describe command's JSON stdout.
func decodeDescribe(t *testing.T, out string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("describe output is not JSON (%v):\n%s", err, out)
	}
	return m
}
