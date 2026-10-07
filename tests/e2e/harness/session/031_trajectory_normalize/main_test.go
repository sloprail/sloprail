package e2e

import (
	"encoding/json"
	"os"
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
// # Tools applied before normalize reads
//
// FILE EVENTS (T031_03). normalize derives create-vs-update by stat-ing the LIVE tree,
// and a mock run APPLIES its writes, so a create reads back as an update. The test
// drives the mock and then puts the tree back to its pre-write state, so the
// trajectory stays the harness's own record rather than a hand-typed one.
//
// PREAMBLE LINES (T031_06) used to keep a fixture too: the physical-line count rests
// on Claude Code's own no-uuid preamble records (custom-title / mode / last-prompt).
// The MOCK now writes those itself on every fresh session — a10n-claude-mock opens a
// fresh transcript with the preamble block ahead of the root, exactly as real Claude
// Code does — so T031_06 drives a plain Run and reads a mock-produced transcript whose
// opening physical lines are real preamble records, no harness or per-test fixture.
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

// normalize runs the compiled binary's normalize against a path and returns
// stdout+exit. Run through CLIDirect (no stdin) since --path makes a payload
// unnecessary and reading stdin in an interactive context would block.
func normalize(e *Env, dir, path string, args ...string) harness.Result {
	full := append([]string{"trajectory", "normalize", "--path", path}, args...)
	return e.CLIDirect(dir, "sr-session", full...)
}

// normalized is one NormalizedEntry decoded from the command's JSON output —
// enough of it to assert on: the line, the raw entry fields the test reaches for,
// and the events with their kind and fields (read FLAT: every key but `kind`).
type normalized struct {
	Type        string          `json:"type"`
	UUID        string          `json:"uuid"`
	IsSidechain bool            `json:"isSidechain"`
	Line        int             `json:"line"`
	Events      []normalizedEvt `json:"events"`
}

type normalizedEvt struct {
	Kind   string
	Fields map[string]interface{}
}

// UnmarshalJSON reads a flat event: `kind` beside the event's own fields.
func (e *normalizedEvt) UnmarshalJSON(b []byte) error {
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	e.Kind, _ = m["kind"].(string)
	delete(m, "kind")
	e.Fields = m
	return nil
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

// eventsOf returns the kinds of an entry's events, in order.
func eventsOf(e normalized) []string {
	kinds := make([]string, 0, len(e.Events))
	for _, ev := range e.Events {
		kinds = append(kinds, ev.Kind)
	}
	return kinds
}

// New is harness.New with the plugin's authoring file-guards switched off: this package
// is about other rules, and the authoring guards would judge the rules' own files.
// NoAutoCheck: these tests read the trajectory the scenario wrote, and the harness's
// pre-Stop `sr-checks run` turn would be a second Bash entry they did not script.
func New(t *testing.T) *Env {
	return harness.New(t, harness.WithoutShippedFileGuards(), harness.NoAutoCheck())
}

// NewJudging is New without NoAutoCheck: the harness's pre-Stop `sr-checks run` is what
// runs a file-guard here, so a test whose subject is what the guard's check reads
// needs it.
func NewJudging(t *testing.T) *Env { return harness.New(t, harness.WithoutShippedFileGuards()) }
