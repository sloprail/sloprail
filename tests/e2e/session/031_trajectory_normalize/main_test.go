package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// trajectory normalize — read a trajectory as normalized entries, each enriched
// with the events re-derived from it. These tests exercise the COMPILED
// sr-session binary as a subprocess.
//
// # Where the trajectories come from
//
// The cases the mock can produce read a mock-generated transcript: the harness
// drives a10n-claude-mock, and `normalize --path` reads the record it wrote — a
// Bash turn for a PreCommandInvoke, a `Say` turn carrying a #tag for a
// PostTagWrite. Keeping the trajectory the mock's makes it deterministic and
// centralised rather than a Claude Code record shape re-typed in this file.
//
// # The shapes the mock cannot produce, and why fixtures stay
//
// Two derivations need a shape the mock's session model does not reach, so they keep
// the minimal hand-authored fixtures below:
//
//   - FILE EVENTS (T031_03). normalize derives create-vs-update by stat-ing the
//     LIVE tree, and a mock run APPLIES its writes before normalize sees it — the
//     mock executes a Write against the working directory — so a create reads back
//     as an update, and an update's oldContent reads back as the new bytes.
//     Measured: a mock Write to an absent path yields PreFileUpdate with oldContent
//     already equal to the written content. The fixture stages the tree in the
//     pre-write state the derivation is about.
//   - PREAMBLE LINES (T031_06). The physical-line count rests on Claude Code's own
//     no-uuid preamble records — custom-title, ai-title, mode, queue-operation,
//     last-prompt — and the mock's validator REJECTS every one as an unknown record
//     type (measured), so it cannot emit them. (A uuid-less `system` record is
//     accepted and would exercise the same count-but-skip path, but standing a
//     non-preamble record in for the preamble is less faithful than the hand line.)
//
// # What a10n-cli#470 MADE producible: multi-block assistant turns
//
// The MULTI-BLOCK cases (T031_05's three-tool-call spread-yield, T031_07's text +
// tool_use in one entry) used to keep fixtures "because the scenario API emits one
// tool call per assistant turn." That was about the harness's turn builders, not the
// mock: the mock forwards a multi-block assistant entry VERBATIM (it executes only
// the first tool_use for its synthesised result, but persists the whole entry). So
// the builders grew to match — BashBatch emits N tool_use blocks in one entry,
// SayBash emits a text block and a tool_use in one — and both cases now drive the
// mock (measured: normalize re-derives three PreCommandInvoke from the one BashBatch
// entry, and a PreCommandInvoke + a PostTagWrite from the one SayBash entry).
//
// The SLICE (--whole-session versus the part not yet judged, T031_09) is the other
// mock-driven case: it needs a real session whose read mark has advanced, which
// only a running session produces, so it drives the harness across two cycles.
//
// CombinedOutput merges stdout and stderr, so a read that printed a diagnostic
// would corrupt the JSON. normalize's --path path is deliberately silent on stderr
// (it consults no session state), so res.Output is pure JSON — which the assertions
// here rely on.

type Env = harness.Env

var (
	New       = harness.New
	Turns     = harness.Turns
	Write     = harness.Write
	Bash      = harness.Bash
	Say       = harness.Say
	SayBash   = harness.SayBash
	BashBatch = harness.BashBatch
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// writeTranscript writes a transcript of the given lines into a temp project and
// returns its path. normalize is handed the path directly, so no particular
// directory layout is required — except for the file-event tests, which run the
// binary from a git repo so the extractor's stats resolve (see fileRepo).
func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "normalize-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return writeTranscriptIn(t, dir, lines...)
}

// writeTranscriptIn writes the transcript into a given directory, for the tests
// that need it beside real files (the file-event cases).
func writeTranscriptIn(t *testing.T, dir string, lines ...string) string {
	t.Helper()
	path := filepath.Join(dir, "s-normalize.jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// normalize runs the compiled binary's normalize against a path and returns
// stdout+exit. Run through CLIDirect (no stdin) since --path makes a payload
// unnecessary and reading stdin in an interactive context would block.
func normalize(e *Env, dir, path string, args ...string) harness.Result {
	full := append([]string{"trajectory", "normalize", "--path", path}, args...)
	return e.CLIDirect(dir, "sr-session", full...)
}

// normalized is one NormalizedEntry decoded from the command's JSON output —
// enough of it to assert on: the line, the raw entry fields the test reaches for,
// and the events with their kind and fields.
type normalized struct {
	Type        string          `json:"type"`
	UUID        string          `json:"uuid"`
	IsSidechain bool            `json:"isSidechain"`
	Line        int             `json:"line"`
	Events      []normalizedEvt `json:"events"`
}

type normalizedEvt struct {
	Kind   string                 `json:"kind"`
	Fields map[string]interface{} `json:"fields"`
}

// decodeEntries parses the command's stdout into the entries, failing the test
// with the raw output when it is not the JSON array expected — which is how a
// stray diagnostic on stdout is caught rather than read as an empty result.
func decodeEntries(t *testing.T, out string) []normalized {
	t.Helper()
	var entries []normalized
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("normalize did not print a JSON array of entries: %v\noutput was:\n%s", err, out)
	}
	return entries
}

// --- fixture record shapes ---

// userMsg is a plain typed user message with string content.
func userMsg(uuid, content string) string {
	return `{"type":"user","uuid":"` + uuid + `","parentUuid":null,"isSidechain":false,` +
		`"message":{"role":"user","content":` + jsonStr(content) + `}}`
}

// assistantBash is an assistant turn whose one block is a Bash tool call.
func assistantBash(uuid, parent, command string) string {
	return assistantBlocks(uuid, parent, false,
		`{"type":"tool_use","id":"`+uuid+`-t","name":"Bash","input":{"command":`+jsonStr(command)+`}}`)
}

// assistantWrite is an assistant turn whose one block is a Write tool call.
func assistantWrite(uuid, parent, path, content string) string {
	return assistantBlocks(uuid, parent, false,
		`{"type":"tool_use","id":"`+uuid+`-t","name":"Write","input":{"file_path":`+jsonStr(path)+`,"content":`+jsonStr(content)+`}}`)
}

// assistantBlocks is an assistant entry carrying the given raw content blocks,
// joined — the general shape the helpers above specialise.
func assistantBlocks(uuid, parent string, sidechain bool, blocks ...string) string {
	return `{"type":"assistant","uuid":"` + uuid + `","parentUuid":"` + parent + `","isSidechain":` +
		boolStr(sidechain) + `,"message":{"role":"assistant","content":[` + strings.Join(blocks, ",") + `]}}`
}

// preambleLine is a Claude Code preamble record carrying no uuid — transcript
// reading skips it as an entry but still counts its physical line, which is what
// the line-number test rests on.
func preambleLine() string {
	return `{"type":"custom-title","customTitle":"a conversation","sessionId":"s"}`
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// jsonStr renders s as a JSON string literal (with surrounding quotes) for
// embedding as a value in a fixture line.
func jsonStr(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}

// dirOf is the directory a fixture transcript sits in — the working directory to
// run the binary from.
func dirOf(path string) string {
	i := strings.LastIndexByte(path, '/')
	if i < 0 {
		return "."
	}
	return path[:i]
}

// eventsOf returns the kinds of an entry's events, in order.
func eventsOf(e normalized) []string {
	kinds := make([]string, 0, len(e.Events))
	for _, ev := range e.Events {
		kinds = append(kinds, ev.Kind)
	}
	return kinds
}
