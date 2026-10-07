package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const touchGate = `on:
  - event: PreCommandInvoke
    match: any(event.invocations, .bin == "touch")
require:
  - citation: {source_types: [user]}
checks:
  - script: ./record.sh
`

func readProj(t *testing.T, proj, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(proj, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// T041_08: a gated command with no cite in front of it is refused, and the
// refusal says how to chain one.
func TestT041_08_UncitedCommandIsRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "grounded-touch", touchGate, map[string]string{"record.sh": recordScript})
	e.CommitAll(proj, "baseline")

	res := e.Run(proj, "s-041-08", prompt, Turns("done", Bash("b1", `touch released.txt`)))
	if !res.Refused() || e.Exists(proj, "released.txt") {
		t.Fatalf("an uncited gated command was not refused:\n%s", res.Output)
	}
	if !res.Saw("sr-session trajectory cite") {
		t.Errorf("the refusal does not say how to chain a citation:\n%s", res.Output)
	}
}

// T041_09: `sr-session trajectory cite '<quote>' && <cmd>` — commandmod reads the
// cite invocation off the line, the session resolves it, and the gate is handed
// the citation.
func TestT041_09_CiteChainGroundsTheCommand(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "grounded-touch", touchGate, map[string]string{"record.sh": recordScript})
	e.CommitAll(proj, "baseline")

	res := e.Run(proj, "s-041-09", prompt, Turns("done",
		Bash("b1", `sr-session trajectory cite 'adopt a decision log' && touch released.txt`),
	))
	if res.Refused() {
		t.Fatalf("a cited command was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, "released.txt") {
		t.Fatalf("the cited command did not run:\n%s", res.Output)
	}
	lines := e.GateLedgerLines(proj, "grounded-touch", "ledger")
	if len(lines) == 0 || !strings.Contains(lines[0], `"quote":"adopt a decision log"`) || !strings.Contains(lines[0], `"n":1`) {
		t.Errorf("the gate was not handed the citation: %v", lines)
	}
}

// T041_10: a cite chain whose quote does not resolve grounds nothing.
// sr:proves citations/quote-resolves-to-exactly-one-entry
func TestT041_10_UnresolvedCiteChainIsRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "grounded-touch", touchGate, map[string]string{"record.sh": recordScript})
	e.CommitAll(proj, "baseline")

	res := e.Run(proj, "s-041-10", prompt, Turns("done",
		Bash("b1", `sr-session trajectory cite 'never said' ; touch released.txt`),
	))
	if !res.Refused() || e.Exists(proj, "released.txt") {
		t.Fatalf("a command behind an unresolved cite was not refused:\n%s", res.Output)
	}
}

// T041_11: source_types narrows the accepted pool — a gate requiring tool_result
// refuses the user's words and accepts a tool's output.
// sr:proves citations/pool-is-not-borrowed
func TestT041_11_SourceTypesSelectThePool(t *testing.T) {
	const gate = `on:
  - event: PreCommandInvoke
    match: any(event.invocations, .bin == "touch")
require:
  - citation:
      source_types: [tool_result]
`
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "proven-touch", gate, nil)
	e.CommitAll(proj, "baseline")

	res := e.Run(proj, "s-041-11", prompt, Turns("done",
		Bash("b0", `echo BUILD-GREEN-7731`),
		Bash("b1", `sr-session trajectory cite 'adopt a decision log' && touch a.txt`),
		Bash("b2", `sr-session trajectory cite --source-types tool_result 'BUILD-GREEN-7731' && touch b.txt`),
	))
	if e.Exists(proj, "a.txt") {
		t.Errorf("a user quote satisfied a tool_result requirement:\n%s", res.Output)
	}
	if !e.Exists(proj, "b.txt") {
		t.Errorf("a tool_result citation did not satisfy the requirement:\n%s", res.Output)
	}
}

// T041_12: requiring a citation on an event that can never carry one is a load
// error, not a rule that refuses forever.
func TestT041_12_CitationOnStopIsALoadError(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "never", "on:\n  - event: Stop\nrequire:\n  - citation: {source_types: [user]}\n", nil)
	e.Gate(proj, "typo", "on:\n  - event: PreCommandInvoke\nrequire:\n  - citation:\n      sourcetypes: [user]\n", nil)

	res := e.CLIDirect(proj, "sr-file", "declarations", proj)
	if res.Code == 0 {
		t.Fatalf("invalid citation requirements loaded clean:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "Stop carries no citations") {
		t.Errorf("the Stop gate's fault is not named:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, `unknown key "sourcetypes"`) {
		t.Errorf("the misspelled key is not named:\n%s", res.Output)
	}
}
