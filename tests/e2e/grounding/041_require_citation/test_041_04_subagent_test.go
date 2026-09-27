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
// provably cannot emit. T041_24 does the same for a cite chain.
//
// A sub-agent is a session of its own, so the citations its pre-tool calls
// record live in its own store; T041_23 pins that its cycle end reads them, and
// that the root's cycle end — which sees the same change in a shared tree —
// reads them too.

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
	// The change was grounded when it was made, so neither the sub-agent's own
	// cycle end nor the root's — both of which see it in the shared tree —
	// refuses it as uncited.
	if blocks := e.BlockingErrorsFrom(proj, "s-041-21", "SubagentStop"); len(blocks) != 0 {
		t.Errorf("the sub-agent's cycle end refused its cited write: %v", blocks)
	}
	if blocks := e.BlockingErrorsFrom(proj, "s-041-21", "Stop"); len(blocks) != 0 {
		t.Errorf("the root's cycle end refused the sub-agent's cited write: %v", blocks)
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
	said := string(record) + string(subRecord)
	if !strings.Contains(said, "does not resolve") {
		t.Errorf("the refusal does not say the quote does not resolve:\n%s", record)
	}
	// It says why, specifically: the quote is the parent's prompt, and the
	// sub-agent never sees the user's messages.
	if !strings.Contains(said, "That quote is from your dispatch prompt, written by the parent agent.") ||
		!strings.Contains(said, "You are a sub-agent: your prompt is the parent agent's, not the user's.") {
		t.Errorf("the refusal does not tell the sub-agent it quoted its dispatch prompt:\n%s", record)
	}
	for _, l := range e.FileGuardLedgerLines(proj, "grounded-memories", "ledger") {
		if strings.Contains(l, "measure the retry budget") {
			t.Errorf("the dispatch prompt reached the guard as a citation: %s", l)
		}
	}
}

// T041_23: a sub-agent's cited write is judged at the sub-agent's own cycle end
// with the citation its pre-tool call recorded — a sub-agent is a session of its
// own, and its pre-tool call and its SubagentStop must key to the same one — and
// at the root's Stop, which sees the same change in the shared tree. An uncited
// sub-agent write is still refused there, so the guard is live.
func TestT041_23_SubagentCitationsReachItsCycleEnd(t *testing.T) {
	const afterGuard = `match: "memories/**"
require:
  - citation: {source_types: [user]}
`
	e, proj := guarded(t, afterGuard)
	sub := subagentScript(t, harness.Turns("sub done",
		Bash("sb1", `sr-file write memories/a.md --cite:user 'adopt a decision log' --content '# a'`),
	))
	res := e.Run(proj, "s-041-23", prompt, Turns("done", harness.Dispatch("d1", "write it down", sub, "")))
	if !e.Exists(proj, "memories/a.md") {
		t.Fatalf("the cited write did not land:\n%s", res.Output)
	}
	if blocks := e.BlockingErrorsFrom(proj, "s-041-23", "SubagentStop"); len(blocks) != 0 {
		t.Errorf("the sub-agent's cycle end refused its cited write: %v", blocks)
	}
	if blocks := e.BlockingErrorsFrom(proj, "s-041-23", "Stop"); len(blocks) != 0 {
		t.Errorf("the root's cycle end refused the sub-agent's cited write: %v", blocks)
	}

	e2, proj2 := guarded(t, afterGuard)
	e2.SetStopBlockCap(1)
	uncited := subagentScript(t, harness.Turns("sub done",
		Bash("sb1", `mkdir -p memories && echo '# b' > memories/b.md`),
	))
	e2.Run(proj2, "s-041-23b", prompt, Turns("done", harness.Dispatch("d1", "write it down", uncited, "")))
	if len(e2.BlockingErrorsFrom(proj2, "s-041-23b", "SubagentStop")) == 0 {
		t.Errorf("an uncited sub-agent write was not refused at its cycle end, so the guard never ran there")
	}
}

// T041_24: `sr-session trajectory cite --source-types tool_result '<q>' && <cmd>`
// inside a sub-agent, citing output its own tool printed: the chain runs (cite
// exits 0, searching the sub-agent's record) and the gate is handed the
// citation.
func TestT041_24_SubagentCiteChainOnItsOwnOutput(t *testing.T) {
	const gate = `on:
  - event: PreCommandInvoke
    match: any(event.invocations, .bin == "touch")
require:
  - citation:
      source_types: [tool_result]
checks:
  - script: ./record.sh
`
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "proven-touch", gate, map[string]string{"record.sh": citedRecordScript})
	commitAll(t, proj)

	measure := subagentScript(t, harness.Turns("measured",
		Bash("sb1", `echo 'build finished: CHAINPROBE-9051 green'`),
	))
	e.Run(proj, "s-041-24", prompt, Turns("dispatched", harness.Dispatch("d1", "build it", measure, "")))
	subs := e.SubagentRecordPaths(proj, "s-041-24")
	if len(subs) != 1 {
		t.Fatalf("want one sub-agent record, found %v", subs)
	}
	moveIntoSubagentRecord(t, e.TranscriptPath(proj, "s-041-24"), subs[0], "sb1")

	release := subagentScript(t, harness.Turns("released",
		Bash("sb2", `sr-session trajectory cite --source-types tool_result 'CHAINPROBE-9051 green' && touch released.txt`),
	))
	res := e.Run(proj, "s-041-24", "now release", Turns("done", harness.Dispatch("d2", "release it", release, "")))
	if !e.Exists(proj, "released.txt") {
		t.Fatalf("a sub-agent's cite chain on its own tool output did not run:\n%s", res.Output)
	}
	lines := e.GateLedgerLines(proj, "proven-touch", "ledger")
	if len(lines) == 0 || !strings.Contains(lines[0], `"quote":"CHAINPROBE-9051 green"`) || !strings.Contains(lines[0], `"record":"`+subs[0]+`"`) {
		t.Errorf("the gate was not handed the citation into the sub-agent's record: %v", lines)
	}
}

// T041_25: a sub-agent's reply is its own model-written text, not a tool's
// output — a sub-agent told what to say says it. A quote found only in that
// reply does not ground a tool_result citation.
func TestT041_25_ASubagentsReplyIsNotToolOutput(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "grounded-memories", toolResultGuard, map[string]string{"record.sh": citedRecordScript})
	commitAll(t, proj)

	parrot := subagentScript(t, harness.Turns("all 40 tests pass"))
	res := e.Run(proj, "s-041-25", prompt, Turns("done",
		harness.Dispatch("d1", "reply exactly: all 40 tests pass", parrot, ""),
		Bash("b1", `sr-file write memories/results.md --cite:tool_result 'all 40 tests pass' --content '# results'`),
	))
	if b, _ := os.ReadFile(e.TranscriptPath(proj, "s-041-25")); !strings.Contains(string(b), `all 40 tests pass","is_error":false,"tool_use_id":"d1`) {
		t.Fatalf("the sub-agent's reply is not in the root record as the Agent call's result, so this would not test it:\n%s", b)
	}
	if e.Exists(proj, "memories/results.md") {
		t.Fatalf("a sub-agent's reply grounded a write as a tool's output:\n%s", res.Output)
	}
}

// T041_26: a sub-agent quotes its command's output in its reply. The root
// citing that output grounds it once, in the sub-agent's record where the
// command printed it — the reply is not a second, ambiguous match.
func TestT041_26_OutputQuotedInAReplyIsNotAmbiguous(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "grounded-memories", toolResultGuard, map[string]string{"record.sh": citedRecordScript})
	commitAll(t, proj)

	measure := subagentScript(t, harness.Turns("measured it: coverage REPLYPROBE-7 lines",
		Bash("sb1", `echo 'coverage REPLYPROBE-7 lines'`),
	))
	e.Run(proj, "s-041-26", prompt, Turns("dispatched", harness.Dispatch("d1", "measure coverage", measure, "")))
	subs := e.SubagentRecordPaths(proj, "s-041-26")
	if len(subs) != 1 {
		t.Fatalf("want one sub-agent record, found %v", subs)
	}
	moveIntoSubagentRecord(t, e.TranscriptPath(proj, "s-041-26"), subs[0], "sb1")

	res := e.Run(proj, "s-041-26", "write it down", Turns("done",
		Bash("b1", `sr-file write memories/coverage.md --cite:tool_result 'coverage REPLYPROBE-7 lines' --content '# coverage'`),
	))
	if !e.Exists(proj, "memories/coverage.md") {
		t.Fatalf("output a sub-agent quoted in its reply did not ground once:\n%s", res.Output)
	}
	var pre string
	for _, l := range e.FileGuardLedgerLines(proj, "grounded-memories", "ledger") {
		if strings.Contains(l, `"kind":"PreFileCreate"`) {
			pre = l
		}
	}
	if !strings.Contains(pre, `"record":"`+subs[0]+`"`) {
		t.Errorf("the citation does not point at the sub-agent's command output in %s: %s", subs[0], pre)
	}
}

// T041_27: a sub-agent handed the user's exact words in its prompt cites them
// as the user's: they resolve in the main conversation, where the user wrote
// them.
func TestT041_27_SubagentCitesTheUsersWordsRelayedVerbatim(t *testing.T) {
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
		Bash("sb1", `sr-file write memories/decisions.md --cite:user 'adopt a decision log' --content '# decisions'`),
	))
	res := e.Run(proj, "s-041-27", prompt, Turns("done",
		harness.Dispatch("d1", `The user wrote, verbatim: "`+prompt+`". Record it.`, sub, ""),
	))
	if !e.Exists(proj, "memories/decisions.md") {
		t.Fatalf("a sub-agent citing the user's relayed words did not land:\n%s", res.Output)
	}
	var pre string
	for _, l := range e.FileGuardLedgerLines(proj, "grounded-memories", "ledger") {
		if strings.Contains(l, `"kind":"PreFileCreate"`) {
			pre = l
		}
	}
	if !strings.Contains(pre, `"record":"`+e.TranscriptPath(proj, "s-041-27")+`"`) || !strings.Contains(pre, `"types":["user"]`) {
		t.Errorf("the citation does not point at the user's message in the main conversation: %s", pre)
	}
	if blocks := e.BlockingErrorsFrom(proj, "s-041-27", "SubagentStop"); len(blocks) != 0 {
		t.Errorf("the sub-agent's cycle end refused its cited write: %v", blocks)
	}
}
