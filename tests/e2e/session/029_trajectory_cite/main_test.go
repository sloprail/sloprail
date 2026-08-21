package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// trajectory cite — turn a substring of the user's own words into a resolvable
// <path>:<line> citation. These tests exercise the COMPILED sr-session binary as
// a subprocess, and the assertions that matter most are on the EXIT CODE: an agent
// scripts against it and branches without parsing stdout.
//
//	exactly one match   the resolvable <path>:<line> on stdout, exit 0
//	several matches     every candidate <path>:<line>, one per line, exit 2
//	no match            nothing on stdout, exit 1
//
// # Where the trajectories come from
//
// The cases the mock can produce read a mock-generated transcript: the harness
// drives a10n-claude-mock, whose seeded prompt IS the user's own words a citable
// message carries, and whose `Say` turn is the agent's own prose that cite must
// NEVER treat as citable. Handing `cite --path` the record the mock wrote keeps the
// trajectory deterministic and centralised in the mock rather than re-typed here.
//
// # A genuine multi-human-turn transcript: two same-session-id runs
//
// A real second human turn is simply a new prompt on the SAME session. The mock now
// models exactly that: a fresh run (--session-id) seeds the `-p` prompt as the
// transcript's parentless ROOT, and a RESUME run (--resume, the same session id)
// APPENDS its prompt as a continuation human record — its own uuid and a non-null
// parentUuid, after the prior cycle's records. The harness drives a repeat Run on one
// session id as that resume automatically, so `e.Run(proj, id, p1, …)` followed by
// `e.Run(proj, id, p2, …)` builds a two-human-turn transcript with the agent's own
// turns in between — precisely the shape the ambiguous case (T029_02) needs. This
// replaced the earlier synthetic single-turn workaround (a builder that co-emitted an
// assistant record and a second human record in one cycle); a real second run is the
// honest shape and needs no hand-authored fixture.
//
// # The shape #470 MADE producible: the AskUserQuestion answer envelope
//
// An AskUserQuestion ANSWER lands as a user record whose content is a `tool_result`
// block, `The user answered: "<question>"="<answer>". ...`. The mock used to reject
// any script-emitted user record containing a tool_result; a10n-cli#470 taught its
// validator that a tool_result WITH a `tool_use_id` is a genuine Claude Code shape
// (the motivating case being exactly this answer envelope) and now forwards +
// persists it into the transcript — only an id-LESS tool_result stays rejected as
// malformed. So the answer-envelope cases (T029_04/05/07/08) are driven through the
// mock via the AnswerQuestion turn builder, not hand-authored fixtures.

type Env = harness.Env

var (
	New            = harness.New
	Turns          = harness.Turns
	Bash           = harness.Bash
	Say            = harness.Say
	AnswerQuestion = harness.AnswerQuestion
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// cite runs the compiled binary's cite against a path and returns stdout+exit.
// Run through CLIDirect (no stdin) since --path makes a payload unnecessary and
// reading stdin in an interactive context would block.
func cite(e *Env, dir, path, quote string) harness.Result {
	return e.CLIDirect(dir, "sr-session", "trajectory", "cite", "--path", path, quote)
}

// writeSubagentTranscript writes a SUB-AGENT's transcript — one whose own origin
// record carries isSidechain, exactly as Claude Code writes a sub-agent's file —
// laid out at <session>/subagents/agent-<id>.jsonl the way the harness nests it,
// and returns its path. This is a trajectory cite must REFUSE: its "user" messages
// are the parent's dispatch, not the end user's words.
func writeSubagentTranscript(t *testing.T, agentID string, lines ...string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "cite-sub-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	subDir := filepath.Join(dir, "s-parent", "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir subagents: %v", err)
	}
	path := filepath.Join(subDir, "agent-"+agentID+".jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write subagent transcript: %v", err)
	}
	return path
}

// sidechainUserMsg is a sub-agent's user record — the shape a sub-agent's own
// transcript carries: a parentless origin with isSidechain true, its content the
// PARENT agent's dispatch prompt rather than anything the end user typed. It is
// precisely what cite must not let a claim be grounded in.
func sidechainUserMsg(uuid, agentID, content string) string {
	return `{"type":"user","uuid":"` + uuid + `","parentUuid":null,"isSidechain":true,` +
		`"agentId":"` + agentID + `","message":{"role":"user","content":` + jsonStr(content) + `}}`
}

// writeSubagentWithMeta writes a sub-agent transcript recognised by its META
// COMPANION rather than by isSidechain — the primary mark Claude Code leaves: an
// agent-<id>.meta.json beside agent-<id>.jsonl. The record's own lines are written
// as given (here with isSidechain false, to prove the meta file alone settles it),
// and the path to the record is returned.
func writeSubagentWithMeta(t *testing.T, agentID string, lines ...string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "cite-meta-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	subDir := filepath.Join(dir, "s-parent", "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir subagents: %v", err)
	}
	path := filepath.Join(subDir, "agent-"+agentID+".jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write subagent transcript: %v", err)
	}
	meta := `{"agentType":"general-purpose","description":"delegated task","toolUseId":"tool-1","spawnDepth":1}`
	metaPath := filepath.Join(subDir, "agent-"+agentID+".meta.json")
	if err := os.WriteFile(metaPath, []byte(meta), 0o644); err != nil {
		t.Fatalf("write subagent meta: %v", err)
	}
	return path
}

// --- fixture record shapes, for the sub-agent cite-refusal cases ---
//
// The answer-envelope shapes used to live here for T029_04/05/07/08; a10n-cli#470
// made the mock forward a tool_result-in-user record, so those tests drive the mock
// via harness.AnswerQuestion. The several-messages case (T029_02) used to keep a
// fixture too; the mock now appends a resume's prompt as a continuation human turn,
// so it drives the mock with TWO same-session-id runs (see the package doc). What
// remains is only the sub-agent cite-refusal fixtures below — and userMsg, which one
// of them (the meta-companion case, T029_12) still needs because it requires a record
// with isSidechain FALSE while the mock's sub-agent origin is always isSidechain true
// (see T029_12's note).

// userMsg is a plain typed user message with string content.
func userMsg(uuid, content string) string {
	return `{"type":"user","uuid":"` + uuid + `","parentUuid":null,"isSidechain":false,` +
		`"message":{"role":"user","content":` + jsonStr(content) + `}}`
}

// jsonStr renders s as a JSON string literal (with surrounding quotes) for
// embedding as a value in a fixture line.
func jsonStr(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}
