package e2e

import (
	"encoding/json"
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
// The mock writes a sub-agent's records there too, so the citation can only
// resolve by the sub-agent's record being searched; requireInSubagentRecord
// checks that layout before each citing call rather than trusting it.
//
// A sub-agent is a session of its own, so the citations its pre-tool calls
// record live in its own store; T041_23 pins that its cycle end reads them, and
// that the root's cycle end — which sees the same change in a shared tree —
// reads them too.

// Where a harness cannot tie a sub-agent to the conversation that dispatched it
// (CapSubagentParentLink absent: Cursor), nothing a sub-agent cites can resolve and the
// main agent cannot cite what a sub-agent's tools printed. The same tests then assert
// that: the cited write does not land. Which branch runs is the driver's declared
// capability, keyed by SR_HARNESS; no test skips for it.

// toolResultGate refuses, before it lands, a write to memories/ that does not cite
// a tool's output; toolResultGuard is the same requirement judged on the settled
// file at Stop. The two halves of one rule share a name.
const toolResultGate = `on:
  - event: PreFileWrite
    match: event.path startsWith "memories/"
require:
  - citation: {source_types: [tool_result]}
checks:
  - script: ./record.sh
`

const toolResultGuard = `match: "memories/**"
require:
  - citation: {source_types: [tool_result]}
checks:
  - script: ./record.sh
`

const userGate = `on:
  - event: PreFileWrite
    match: event.path startsWith "memories/"
require:
  - citation: {source_types: [user]}
checks:
  - script: ./record.sh
`

const userGuard = `match: "memories/**"
require:
  - citation: {source_types: [user]}
checks:
  - script: ./record.sh
`

// installBoth installs a grounded rule as its gate and its plain file-guard, both
// recording what their check was handed.
func installBoth(e *harness.Env, proj, gate, guard string) {
	e.Gate(proj, "grounded-memories", gate, map[string]string{"record.sh": citedGateRecordScript})
	e.FileGuard(proj, "grounded-memories", guard, map[string]string{"record.sh": citedRecordScript})
}

// bothLedger is what both halves' checks were handed: the gate's, then the guard's.
func bothLedger(e *harness.Env, proj string) []string {
	return append(e.GateLedgerLines(proj, "grounded-memories", "ledger"),
		e.FileGuardLedgerLines(proj, "grounded-memories", "ledger")...)
}

// citedRecordScript records, beside the quote, which record the citation
// resolved in and in which pool.
const citedRecordScript_ = `#!/usr/bin/env bash
set -uo pipefail
payload="$(cat)"
printf '%s' "$payload" | jq -c '{kind: .event.kind, path: (.event.path // ""), n: (.event.citations | length), quote: (.event.citations[0].quote // ""), record: (.event.citations[0].path // ""), types: (.event.citations[0].sourceTypes // [])}' >> "$SR_GUARDRAIL_DIR/ledger"`
const citedRecordScript = citedRecordScript_ + `
exit 0
`

// citedGateRecordScript is citedRecordScript for a gate: after recording, it
// refuses a create or update whose result the engine could not compute.
const citedGateRecordScript = citedRecordScript_ + `
case "$(printf '%s' "$payload" | jq -r '.event.kind')" in
  PreFileCreate|PreFileUpdate)
    [ "$(printf '%s' "$payload" | jq -r '.event.resultKnown')" = "true" ] || {
      echo '{"reason":"the result of this write could not be computed, so it cannot be checked before it lands"}'
      exit 1
    } ;;
esac
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

// requireInSubagentRecord checks that the sub-agent's tool call and its result
// are in the sub-agent's own record and not in the root's — the layout Claude
// Code writes, and the one that makes a citation of that output a test of the
// sub-agent's record being searched.
func requireInSubagentRecord(t *testing.T, root, sub, callID string) {
	t.Helper()
	count := func(path string) int {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		n := 0
		for _, l := range strings.Split(string(body), "\n") {
			if strings.Contains(l, `"`+callID+`-slop-turn`) {
				n++
			}
		}
		return n
	}
	if n := count(sub); n != 2 {
		t.Fatalf("expected the sub-agent's call and its result in its own record, found %d lines", n)
	}
	if n := count(root); n != 0 {
		t.Fatalf("the sub-agent's call is in the root record (%d lines), so this would not test the sub-agent's", n)
	}
}

// T041_21: a sub-agent runs a command, then writes a guarded file citing that
// command's output as tool_result; the write lands, grounded in the
// sub-agent's own record.
func TestT041_21_SubagentCitesItsOwnToolOutput(t *testing.T) {
	linked := harness.HasCap(t, harness.CapSubagentParentLink)
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installBoth(e, proj, toolResultGate, toolResultGuard)
	e.CommitAll(proj, "baseline")

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
	if linked {
		requireInSubagentRecord(t, root, subs[0], "sb1")
	}
	if b, _ := os.ReadFile(root); strings.Contains(string(b), "SUBPROBE-4417") {
		t.Fatalf("the output is still in the root record, so this would not test the sub-agent's")
	}

	// And a sub-agent grounds the finding in that output.
	write := subagentScript(t, harness.Turns("written",
		Bash("sb2", `sr-file write memories/findings.md --cite:tool_result 'SUBPROBE-4417 attempts' --content '# findings'`),
		harness.Commit("sc2", "write down the retry budget", harness.CitesTool("SUBPROBE-4417 attempts")),
	))
	res = e.Run(proj, "s-041-21", "now write it down", Turns("done",
		harness.Dispatch("d2", "write down the retry budget", write, ""),
	))
	if !linked {
		if e.Exists(proj, "memories/findings.md") {
			t.Fatalf("a sub-agent's write was grounded on a harness that cannot link it to its session:\n%s", res.Output)
		}
		return
	}
	if !e.Exists(proj, "memories/findings.md") {
		t.Fatalf("a sub-agent's write citing its own tool output did not land:\n%s", res.Output)
	}

	var pre string
	for _, l := range bothLedger(e, proj) {
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
	// The sub-agent committed with a trailer citing its own tool output, which the
	// session resolves against the sub-agent's record. The tree is shared, so it is
	// the root's Stop that judges the range — and it finds the citation.
	if blocks := e.AnySubagentBlockingErrors(proj, "s-041-21"); len(blocks) != 0 {
		t.Errorf("the sub-agent's cycle end refused its cited write: %v", blocks)
	}
	if blocks := e.BlockingErrorsFrom(proj, "s-041-21", "Stop"); len(blocks) != 0 {
		t.Errorf("the root's cycle end refused the sub-agent's cited write: %v", blocks)
	}
}

// T041_22: a sub-agent citing its dispatch prompt as the user's words is
// refused — those are the parent agent's words — and nothing lands.
// sr:proves citations/user-pool-is-the-root-conversation
func TestT041_22_SubagentCannotCiteItsDispatchAsTheUser(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installBoth(e, proj, userGate, userGuard)
	e.CommitAll(proj, "baseline")

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
	// A harness whose record holds no tool results (and a refusal is one) cannot show the
	// refusal's wording; that the write did not land is shown above.
	wording := harness.HasCap(t, harness.CapRecordHoldsToolResults)
	if wording && !strings.Contains(said, "does not resolve") {
		t.Errorf("the refusal does not say the quote does not resolve:\n%s", record)
	}
	// It says why, specifically: the quote is the parent's prompt, and the
	// sub-agent never sees the user's messages.
	if wording && (!strings.Contains(said, "That quote is from your dispatch prompt, written by the parent agent.") ||
		!strings.Contains(said, "You are a sub-agent: your prompt is the parent agent's, not the user's.")) {
		t.Errorf("the refusal does not tell the sub-agent it quoted its dispatch prompt:\n%s", record)
	}
	for _, l := range bothLedger(e, proj) {
		if strings.Contains(l, "measure the retry budget") {
			t.Errorf("the dispatch prompt reached the guard as a citation: %s", l)
		}
	}
}

// T041_23: a shared-tree sub-agent's cited commit is judged at the ROOT's Stop —
// the sub-agent owns none of the tree, so its own stop judges nothing — and the
// trailer it committed with grounds the range. An uncited sub-agent commit is
// refused there, so the guard is live.
func TestT041_23_SubagentCitationsReachTheRootsStop(t *testing.T) {
	const afterGuard = `match: "memories/**"
require:
  - citation: {source_types: [user]}
`
	e, proj := guarded(t, afterGuard)
	sub := subagentScript(t, harness.Turns("sub done",
		harness.CommitFile("sb1", "memories/a.md", "# a", "write it down", harness.CitesUser("adopt a decision log")),
	))
	res := e.Run(proj, "s-041-23", prompt, Turns("done", harness.Dispatch("d1", "write it down", sub, "")))
	if !e.Exists(proj, "memories/a.md") {
		t.Fatalf("the cited write did not land:\n%s", res.Output)
	}
	if blocks := e.AnySubagentBlockingErrors(proj, "s-041-23"); len(blocks) != 0 {
		t.Errorf("the sub-agent's cycle end refused its cited write: %v", blocks)
	}
	if blocks := e.BlockingErrorsFrom(proj, "s-041-23", "Stop"); len(blocks) != 0 {
		t.Errorf("the root's cycle end refused the sub-agent's cited write: %v", blocks)
	}

	e2, proj2 := guarded(t, afterGuard)
	e2.SetStopBlockCap(1)
	uncited := subagentScript(t, harness.Turns("sub done",
		harness.CommitFile("sb1", "memories/b.md", "# b", "write it down"),
	))
	e2.Run(proj2, "s-041-23b", prompt, Turns("done", harness.Dispatch("d1", "write it down", uncited, "")))
	if blocks := stopRefusal(e2, proj2, "s-041-23b"); !strings.Contains(blocks, noCitation) {
		t.Errorf("an uncited sub-agent commit was not refused at the root's Stop, so the guard never ran:\n%s", blocks)
	}
	if blocks := e2.SubagentBlockingErrors(proj2, "s-041-23b"); len(blocks) != 0 {
		t.Errorf("the sub-agent's own stop judged a range it does not own: %v", blocks)
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
	e.CommitAll(proj, "baseline")

	measure := subagentScript(t, harness.Turns("measured",
		Bash("sb1", `echo 'build finished: CHAINPROBE-9051 green'`),
	))
	e.Run(proj, "s-041-24", prompt, Turns("dispatched", harness.Dispatch("d1", "build it", measure, "")))
	subs := e.SubagentRecordPaths(proj, "s-041-24")
	if len(subs) != 1 {
		t.Fatalf("want one sub-agent record, found %v", subs)
	}
	linked := harness.HasCap(t, harness.CapSubagentParentLink)
	if linked {
		requireInSubagentRecord(t, e.TranscriptPath(proj, "s-041-24"), subs[0], "sb1")
	}

	release := subagentScript(t, harness.Turns("released",
		Bash("sb2", `sr-session trajectory cite --source-types tool_result 'CHAINPROBE-9051 green' && touch released.txt`),
	))
	res := e.Run(proj, "s-041-24", "now release", Turns("done", harness.Dispatch("d2", "release it", release, "")))
	if !linked {
		// cite errors from a sub-agent, so the chain stops and the gate is never handed a citation.
		if e.Exists(proj, "released.txt") {
			t.Fatalf("a cite chain ran in a sub-agent of a harness that cannot link it to its session:\n%s", res.Output)
		}
		if lines := e.GateLedgerLines(proj, "proven-touch", "ledger"); len(lines) != 0 {
			t.Fatalf("the gate was handed a citation from a sub-agent that cannot cite: %v", lines)
		}
		return
	}
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
// sr:proves citations/tool-result-pool-is-genuine-tool-output
func TestT041_25_ASubagentsReplyIsNotToolOutput(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installBoth(e, proj, toolResultGate, toolResultGuard)
	e.CommitAll(proj, "baseline")

	parrot := subagentScript(t, harness.Turns("all 40 tests pass"))
	res := e.Run(proj, "s-041-25", prompt, Turns("done",
		harness.Dispatch("d1", "reply exactly: all 40 tests pass", parrot, ""),
		Bash("b1", `sr-file write memories/results.md --cite:tool_result 'all 40 tests pass' --content '# results'`),
	))
	linked := harness.HasCap(t, harness.CapSubagentParentLink)
	if !linked {
		// The reply is nowhere citable and the sub-agent is not linked: the write is refused.
		if e.Exists(proj, "memories/results.md") {
			t.Fatalf("a sub-agent's reply grounded a write as a tool's output:\n%s", res.Output)
		}
		return
	}
	if body := agentResultBody(t, e.TranscriptPath(proj, "s-041-25"), "d1"); !strings.Contains(body, "all 40 tests pass") {
		t.Fatalf("the sub-agent's reply is not in the root record as the Agent call's result, so this would not test it:\n%s", body)
	}
	if e.Exists(proj, "memories/results.md") {
		t.Fatalf("a sub-agent's reply grounded a write as a tool's output:\n%s", res.Output)
	}
	// The words ARE in the record, so "not there word for word" would send the
	// agent looking for a typo: the failure says what those words are.
	if !res.Saw("a sub-agent's reply (model-written)") {
		t.Errorf("the failed citation does not say the quote is a sub-agent's reply:\n%s", res.Output)
	}
}

// T041_26: a sub-agent quotes its command's output in its reply. The root
// citing that output grounds it once, in the sub-agent's record where the
// command printed it — the reply is not a second, ambiguous match.
// sr:proves citations/quote-resolves-to-exactly-one-entry
func TestT041_26_OutputQuotedInAReplyIsNotAmbiguous(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installBoth(e, proj, toolResultGate, toolResultGuard)
	e.CommitAll(proj, "baseline")

	measure := subagentScript(t, harness.Turns("measured it: coverage REPLYPROBE-7 lines",
		Bash("sb1", `echo 'coverage REPLYPROBE-7 lines'`),
	))
	e.Run(proj, "s-041-26", prompt, Turns("dispatched", harness.Dispatch("d1", "measure coverage", measure, "")))
	subs := e.SubagentRecordPaths(proj, "s-041-26")
	if len(subs) != 1 {
		t.Fatalf("want one sub-agent record, found %v", subs)
	}
	linked := harness.HasCap(t, harness.CapSubagentParentLink)
	if linked {
		requireInSubagentRecord(t, e.TranscriptPath(proj, "s-041-26"), subs[0], "sb1")
	}

	res := e.Run(proj, "s-041-26", "write it down", Turns("done",
		Bash("b1", `sr-file write memories/coverage.md --cite:tool_result 'coverage REPLYPROBE-7 lines' --content '# coverage'`),
	))
	if !linked {
		// The main agent cannot reach a sub-agent's output: nothing to ground the write on.
		if e.Exists(proj, "memories/coverage.md") {
			t.Fatalf("a sub-agent's output grounded a write on a harness that cannot link it:\n%s", res.Output)
		}
		return
	}
	if !e.Exists(proj, "memories/coverage.md") {
		t.Fatalf("output a sub-agent quoted in its reply did not ground once:\n%s", res.Output)
	}
	var pre string
	for _, l := range bothLedger(e, proj) {
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
	harness.RequireCap(t, harness.CapSubagentParentLink)
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installBoth(e, proj, userGate, userGuard)
	e.CommitAll(proj, "baseline")

	sub := subagentScript(t, harness.Turns("sub done",
		Bash("sb1", `sr-file write memories/decisions.md --cite:user 'adopt a decision log' --content '# decisions'`),
	))
	res := e.Run(proj, "s-041-27", prompt, Turns("done",
		harness.Dispatch("d1", `The user wrote, verbatim: "`+prompt+`". Record it.`, sub, ""),
	))
	if !harness.HasCap(t, harness.CapSubagentParentLink) {
		// The sub-agent cannot reach the user's conversation to resolve the words in.
		if e.Exists(proj, "memories/decisions.md") {
			t.Fatalf("a sub-agent cited the user's words on a harness that cannot link it:\n%s", res.Output)
		}
		return
	}
	if !e.Exists(proj, "memories/decisions.md") {
		t.Fatalf("a sub-agent citing the user's relayed words did not land:\n%s", res.Output)
	}
	var pre string
	for _, l := range bothLedger(e, proj) {
		if strings.Contains(l, `"kind":"PreFileCreate"`) {
			pre = l
		}
	}
	if !strings.Contains(pre, `"record":"`+e.TranscriptPath(proj, "s-041-27")+`"`) || !strings.Contains(pre, `"types":["user"]`) {
		t.Errorf("the citation does not point at the user's message in the main conversation: %s", pre)
	}
	if blocks := e.AnySubagentBlockingErrors(proj, "s-041-27"); len(blocks) != 0 {
		t.Errorf("the sub-agent's cycle end refused its cited write: %v", blocks)
	}
}

// agentResultBody is the text of the tool_result answering the Agent call whose
// id starts with callID, in the record at path — the sub-agent's hand-back as
// real Claude Code writes it (a list of text blocks), or a plain string.
func agentResultBody(t *testing.T, path, callID string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var body strings.Builder
	for _, line := range strings.Split(string(b), "\n") {
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &rec) != nil || rec.Type != "user" {
			continue
		}
		var blocks []struct {
			Type      string          `json:"type"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
		}
		if json.Unmarshal(rec.Message.Content, &blocks) != nil {
			continue
		}
		for _, bl := range blocks {
			if bl.Type != "tool_result" || !strings.HasPrefix(bl.ToolUseID, callID) {
				continue
			}
			var s string
			if json.Unmarshal(bl.Content, &s) == nil {
				body.WriteString(s)
				continue
			}
			var texts []struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(bl.Content, &texts) == nil {
				for _, tx := range texts {
					body.WriteString(tx.Text)
				}
			}
		}
	}
	return body.String()
}
