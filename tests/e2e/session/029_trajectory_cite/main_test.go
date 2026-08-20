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
// # The two shapes the mock cannot produce, and why fixtures stay
//
// The mock's session model has exactly ONE human turn — the prompt — so a
// transcript with SEVERAL distinct user messages (the ambiguous case, T029_02) is
// outside what it emits. And an AskUserQuestion ANSWER lands as a user record whose
// content is a `tool_result` block, which the mock REJECTS outright: its scenario
// validator refuses any script-emitted user record containing a tool_result
// ("that is synthesised by the mock after it executes a tool, not by the agent
// script"), verified against a10n-cli's claude-mock. So the answer-envelope cases
// (T029_04/05/07/08) cannot be driven through it either. Those, and the
// several-messages case, keep the minimal hand-authored fixtures below — the exact
// shapes real ~/.claude transcripts carry — with this note saying why.

type Env = harness.Env

var (
	New   = harness.New
	Turns = harness.Turns
	Bash  = harness.Bash
	Say   = harness.Say
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// writeTranscript writes a transcript of the given lines into a temp project and
// returns its path — for the fixture cases the mock cannot produce (several user
// messages, and AskUserQuestion answer envelopes). cite is handed the path
// directly, so no particular directory layout is required.
func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "cite-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "s-cite.jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
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

// --- fixture record shapes ---
// --- fixture record shapes, for the cases the mock cannot emit ---

// userMsg is a plain typed user message with string content.
func userMsg(uuid, content string) string {
	return `{"type":"user","uuid":"` + uuid + `","parentUuid":null,"isSidechain":false,` +
		`"message":{"role":"user","content":` + jsonStr(content) + `}}`
}

// assistantText is an assistant turn whose content is a text block — the agent's
// own words, which cite must never treat as citable.
func assistantText(uuid, parent, text string) string {
	return `{"type":"assistant","uuid":"` + uuid + `","parentUuid":"` + parent + `","isSidechain":false,` +
		`"message":{"role":"assistant","content":[{"type":"text","text":` + jsonStr(text) + `}]}}`
}

// answerEnvelope is a user entry carrying an AskUserQuestion answer, in the exact
// envelope shape a real transcript writes: a tool_result whose content reads
// `The user answered: "<question>"="<answer>". ...`.
//
// The content is built as a PLAIN string with literal quotes around the question
// and answer — that is the envelope's own text — and serialized once by jsonStr,
// which is what turns those quotes into the `\"` a JSON string carries. Building
// them pre-escaped would double-escape once jsonStr ran over them.
func answerEnvelope(uuid, parent, question, answer string) string {
	return multiAnswerEnvelope(uuid, parent, [][2]string{{question, answer}})
}

// multiAnswerEnvelope is the MULTI-question shape: one AskUserQuestion call asks
// several questions and the harness writes every Q/A pair into ONE tool_result
// string, joined by `, ` and closed with the `. Read the answers ...` trailer.
// This is the common real shape (44% of measured envelopes ask more than one),
// and the case the first-join-to-last-quote parser mangled — so the e2e cases
// that prove no question text leaks are built from it.
//
// Each qa is {question, answer}. The content is assembled as a plain string with
// literal quotes and serialized once by jsonStr.
func multiAnswerEnvelope(uuid, parent string, qa [][2]string) string {
	content := `The user answered: `
	for i, p := range qa {
		if i > 0 {
			content += `, `
		}
		content += `"` + p[0] + `"="` + p[1] + `"`
	}
	content += `. Read the answers carefully — they may request clarification, changes, or that you not proceed.`
	return `{"type":"user","uuid":"` + uuid + `","parentUuid":"` + parent + `","isSidechain":false,` +
		`"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":` +
		jsonStr(content) + `}]}}`
}

// jsonStr renders s as a JSON string literal (with surrounding quotes) for
// embedding as a value in a fixture line.
func jsonStr(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}
