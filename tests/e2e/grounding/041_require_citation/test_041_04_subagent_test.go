package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// A sub-agent grounds its work in what ITS OWN tools printed. Claude Code
// records a sub-agent's tool calls and their output only in the sub-agent's own
// record (<session>/subagents/agent-<id>.jsonl), so the tool_result pool
// searches the records of the sub-agents a session dispatched beside the
// root's. The user pool does not: a sub-agent's "user" message is the parent
// agent's dispatch, never the end user's words.
//
// The mock (v0.1.1) writes a sub-agent's tool calls into the ROOT record instead,
// which would let the citation resolve without the sub-agent's record ever
// being searched. So T041_21 moves them to where Claude Code writes them before
// the citing call runs — the one hand-arranged shape here, and the one the mock
// provably cannot emit.

const toolResultGuard = `match: "memories/**"
preventive: true
require:
  - citation: {source_types: [tool_result]}
checks:
  - script: ./record.sh
`

// citedRecordScript records, beside the quote, which record the citation
// resolved in and in which pool.
const citedRecordScript = `#!/usr/bin/env bash
set -uo pipefail
payload="$(cat)"
printf '%s' "$payload" | jq -c '{kind: .event.kind, path: (.event.path // ""), n: (.event.citations | length), quote: (.event.citations[0].quote // ""), record: (.event.citations[0].path // ""), types: (.event.citations[0].sourceTypes // [])}' >> "$SR_GUARDRAIL_DIR/ledger"
exit 0
`

func subagentScript(t *testing.T, s harness.Scenario) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sub.sh")
	if err := s.Script(path); err != nil {
		t.Fatalf("write sub-agent scenario: %v", err)
	}
	return path
}

// moveIntoSubagentRecord moves every line of the root record mentioning the
// sub-agent's tool call id into the sub-agent's own record, marked as a
// sidechain — the layout Claude Code writes.
func moveIntoSubagentRecord(t *testing.T, root, sub, callID string) {
	t.Helper()
	body, err := os.ReadFile(root)
	if err != nil {
		t.Fatalf("read root record: %v", err)
	}
	var kept, moved []string
	for _, l := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
		if strings.Contains(l, `"`+callID+`-slop-turn`) || strings.Contains(l, `"e2e-turn-`+callID+`"`) {
			moved = append(moved, `{"isSidechain":true,`+strings.TrimPrefix(l, "{"))
			continue
		}
		kept = append(kept, l)
	}
	if len(moved) != 2 {
		t.Fatalf("expected the sub-agent's call and its result in the root record, moved %d lines", len(moved))
	}
	if err := os.WriteFile(root, []byte(strings.Join(kept, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("rewrite root record: %v", err)
	}
	f, err := os.OpenFile(sub, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open sub-agent record: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(moved, "\n") + "\n"); err != nil {
		t.Fatalf("append to sub-agent record: %v", err)
	}
}

// T041_21: a sub-agent runs a command, then writes a guarded file citing that
// command's output as tool_result; the write lands, grounded in the
// sub-agent's own record.
func TestT041_21_SubagentCitesItsOwnToolOutput(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "grounded-memories", toolResultGuard, map[string]string{"record.sh": citedRecordScript})
	commitAll(t, proj)
	// The sub-agent's own cycle end is not what this is about; one refusal
	// there is enough to finish the run.
	e.SetStopBlockCap(1)

	// The sub-agent measures something.
	measure := subagentScript(t, harness.Turns("measured",
		Bash("sb1", `echo 'retry budget measured: SUBPROBE-4417 attempts'`),
	))
	res := e.Run(proj, "s-041-21", prompt, Turns("dispatched",
		harness.Dispatch("d1", "measure the retry budget", measure, ""),
	))
	subs := e.SubagentRecordPaths(proj, "s-041-21")
	if len(subs) != 1 {
		t.Fatalf("want one sub-agent record, found %v:\n%s", subs, res.Output)
	}
	root := e.TranscriptPath(proj, "s-041-21")
	moveIntoSubagentRecord(t, root, subs[0], "sb1")
	if b, _ := os.ReadFile(root); strings.Contains(string(b), "SUBPROBE-4417") {
		t.Fatalf("the output is still in the root record, so this would not test the sub-agent's")
	}

	// And a sub-agent grounds the finding in that output.
	write := subagentScript(t, harness.Turns("written",
		Bash("sb2", `sr-file write memories/findings.md --cite:tool_result 'SUBPROBE-4417 attempts' --content '# findings'`),
	))
	res = e.Run(proj, "s-041-21", "now write it down", Turns("done",
		harness.Dispatch("d2", "write down the retry budget", write, ""),
	))
	if !e.Exists(proj, "memories/findings.md") {
		t.Fatalf("a sub-agent's write citing its own tool output did not land:\n%s", res.Output)
	}

	var pre string
	for _, l := range e.FileGuardLedgerLines(proj, "grounded-memories", "ledger") {
		if strings.Contains(l, `"kind":"PreFileCreate"`) {
			pre = l
		}
	}
	if pre == "" {
		t.Fatalf("the guard never judged the sub-agent's write before it landed:\n%s", res.Output)
	}
	if !strings.Contains(pre, `"n":1`) || !strings.Contains(pre, `"quote":"SUBPROBE-4417 attempts"`) || !strings.Contains(pre, `"types":["tool_result"]`) {
		t.Errorf("the guard was not handed the tool_result citation: %s", pre)
	}
	if !strings.Contains(pre, `"record":"`+subs[0]+`"`) {
		t.Errorf("the citation does not point into the sub-agent's own record %s: %s", subs[0], pre)
	}
}

// T041_22: a sub-agent citing its dispatch prompt as the user's words is
// refused — those are the parent agent's words — and nothing lands.
func TestT041_22_SubagentCannotCiteItsDispatchAsTheUser(t *testing.T) {
	const userGuard = `match: "memories/**"
preventive: true
require:
  - citation: {source_types: [user]}
checks:
  - script: ./record.sh
`
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "grounded-memories", userGuard, map[string]string{"record.sh": citedRecordScript})
	commitAll(t, proj)

	sub := subagentScript(t, harness.Turns("sub done",
		Bash("sb1", `sr-file write memories/findings.md --cite:user 'measure the retry budget' --content '# findings'`),
	))
	res := e.Run(proj, "s-041-22", prompt, Turns("done",
		harness.Dispatch("d1", "measure the retry budget", sub, ""),
	))
	subs := e.SubagentRecordPaths(proj, "s-041-22")
	if len(subs) != 1 {
		t.Fatalf("want one sub-agent record, found %v:\n%s", subs, res.Output)
	}
	if b, _ := os.ReadFile(subs[0]); !strings.Contains(string(b), "measure the retry budget") {
		t.Fatalf("the dispatch prompt is not in the sub-agent's record, so this would not test citing it")
	}
	if e.Exists(proj, "memories/findings.md") {
		t.Fatalf("a sub-agent grounded a write in its dispatch prompt as if the user had said it:\n%s", res.Output)
	}
	// The sub-agent's own tool results are not streamed to the root's output;
	// the refusal is read from the session's records.
	record, _ := os.ReadFile(e.TranscriptPath(proj, "s-041-22"))
	subRecord, _ := os.ReadFile(subs[0])
	if !strings.Contains(string(record)+string(subRecord), "does not resolve") {
		t.Errorf("the refusal does not say the quote does not resolve:\n%s", record)
	}
	for _, l := range e.FileGuardLedgerLines(proj, "grounded-memories", "ledger") {
		if strings.Contains(l, "measure the retry budget") {
			t.Errorf("the dispatch prompt reached the guard as a citation: %s", l)
		}
	}
}
