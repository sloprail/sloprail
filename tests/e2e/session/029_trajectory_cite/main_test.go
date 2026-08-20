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
// a subprocess against a real-layout fixture trajectory, and the assertions that
// matter most are on the EXIT CODE: an agent scripts against it and branches
// without parsing stdout.
//
//	exactly one match   the resolvable <path>:<line> on stdout, exit 0
//	several matches     every candidate <path>:<line>, one per line, exit 2
//	no match            nothing on stdout, exit 1
//
// Fixtures rather than the mock, for the same reason describe uses them: cite is
// run by an agent with a path, not fired at a hook pause, so the honest e2e is
// the binary against a fixture. The fixture shapes are taken from real ~/.claude
// transcripts — a plain user message with string content, an assistant text turn
// (whose words must NEVER be citable), and the AskUserQuestion answer envelope a
// prompted answer lands in.

type Env = harness.Env

var New = harness.New

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// writeTranscript writes a transcript of the given lines into a temp project and
// returns its path. cite is handed the path directly, so no particular directory
// layout is required.
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

// --- fixture record shapes ---

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
