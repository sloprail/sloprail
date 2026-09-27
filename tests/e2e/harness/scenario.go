package harness

import (
	"fmt"
	"os"
	"strings"
)

// Scenario is what the agent does: one turn per action.
//
// The mock runs it as a shell script that streams Claude Code JSONL. A turn
// fires once — it writes a marker into the tool_use id, and skips itself if
// that marker is already in the conversation so far. That is what makes a
// multi-turn scenario work: the mock re-runs the script after each tool result,
// and each run emits the first turn that has not gone yet.
type Scenario struct {
	turns  []Turn
	result string
}

// Turn is one assistant action.
type Turn struct {
	jsonl string
}

// Turns builds a scenario ending in the given assistant text.
func Turns(finalText string, turns ...Turn) Scenario {
	return Scenario{turns: turns, result: finalText}
}

// Write returns a turn where the agent writes a file.
func Write(id, path, content string) Turn {
	return Turn{jsonl: toolUse(id, "Write", map[string]string{
		"file_path": path,
		"content":   content,
	})}
}

// Bash returns a turn where the agent runs a shell command.
func Bash(id, command string) Turn {
	return Turn{jsonl: toolUse(id, "Bash", map[string]string{"command": command})}
}

// Say returns a turn where the agent writes plain prose — an assistant message
// carrying a text block rather than a tool call.
//
// This is the shape a `#tag` lives in: the agent says something in its own
// message, and PostTagWrite scans that settled text. A tool_use turn carries no
// prose, so a scenario that only wrote files could never exercise a tag, which
// is why this exists alongside Write and Bash.
//
// The record is a `type:"assistant"` entry whose content is a one-element block
// list `[{"type":"text","text":…}]` — the block-list shape Claude Code writes for
// a turn that has prose in it. It carries a uuid keyed off the turn's id, like
// every other turn, so transcript.Read does not skip it.
func Say(id, text string) Turn {
	return Turn{jsonl: fmt.Sprintf(
		`{"type":"assistant","uuid":%q,"message":{"role":"assistant","stop_reason":null,"content":[{"type":"text","text":%s}]}}`,
		"e2e-turn-"+id, jsonStr(text))}
}

// ToolUse returns a turn where the agent invokes an ARBITRARY tool by name.
//
// Write/Bash/Say cover the tools with special harness handling; this is for a
// trajectory that must carry a tool the mock does not implement — a `fill_form`,
// a `download_file`, a `screenshot` — because a rule reads those tool_use blocks
// out of the record (action-proof's prepare scans `.message.content[]` for a
// `fill_form`/`screenshot` by name). The mock passes the tool_use block through
// into the transcript verbatim (its `.name` and `.input`) and answers the call
// with a not-implemented tool_result; the block itself is what the rule reads,
// which is all a trajectory-reading check needs. The tool_result the mock returns
// carries no `toolUseResult` field, so a check pulling an artifact OUT of the
// result (a screenshot image) sees none — which is the honest "no proof present"
// state, exactly the violation such a rule catches.
//
// input values are strings, which is what these representative tools take; a
// check reading the input as JSON (`.input.email`) reads them as such.
//
// The mock's own synthesised result for the tool carries no `toolUseResult`, so a
// tool WITH a produced artifact a check reads back — a screenshot whose image an
// audit inspects — uses ToolUseWithResult instead, which supplies that field.
func ToolUse(id, name string, input map[string]string) Turn {
	return Turn{jsonl: toolUse(id, name, input)}
}

// ToolUseJSON is ToolUse with the input given as a raw JSON object, for a tool
// whose input carries numbers, nested objects or arrays — values a check must
// carry through as JSON, which a map of strings cannot express.
func ToolUseJSON(id, name, inputJSON string) Turn {
	return Turn{jsonl: fmt.Sprintf(`{"type":"assistant","uuid":%q,"message":{"role":"assistant","stop_reason":null,"content":[{"type":"tool_use","id":%q,"name":%q,"input":%s}]}}`,
		"e2e-turn-"+id, id, name, inputJSON)}
}

// ToolUseWithResult returns the TWO turns that model a tool call which produced an
// artifact a grounding check reads back: the tool_use, and a following record
// carrying its `toolUseResult`.
//
// # Why a produced artifact needs this
//
// A tool call's real output — what a screenshot actually captured, the bytes a
// download produced — lives on the transcript entry's top-level `toolUseResult`
// field (transcript.Entry: "where evidence of what an action actually produced
// lives"), and a grounding check reads it there (action-proof's prepare pulls the
// screenshot's `toolUseResult` to judge whether the proof shows the filled form).
// The mock cannot supply it: for an unimplemented tool (`screenshot`) it synthesises
// an ERROR tool_result whose block carries no `toolUseResult`, and its tool executor
// (toolexec.Result) is only {Output, IsError} with no structured artifact channel.
// So a trajectory that must carry a produced artifact emits the entry itself.
//
// # Why two turns
//
// The mock breaks its read loop at a tool_use to execute the tool and re-invoke the
// script, so any line after the tool_use in the same turn is never read. The
// tool_use is therefore one turn (the mock answers it with its own error result),
// and the artifact record is the next: a user record that ALSO carries a
// `tool_result` block for the same id. Being the LAST tool_result for that id, its
// entry is the one the prepare's `[-1]` selects, and its `toolUseResult` is what the
// check reads.
//
// # Why the ids are shaped this way
//
// The turn-firing marker is injected into the FIRST `"id":"…"` of a turn's record.
// Left to the tool_use block's own `id`, the screenshot's id in the transcript
// would become `<id>-slop-turn-N-<id>` — and the artifact record's `tool_use_id`,
// which is NOT marked, would then fail to correlate. So BOTH records carry a
// throwaway top-level `id` (`<id>#u` / `<id>#r`) for the marker to bind to, which
// keeps the tool_use's own `id` and the block's `tool_use_id` CLEAN and equal to
// `id` — so they correlate. The artifact block carries no `name`, so the mock does
// not treat it as one of its own results.
//
// toolUseResultJSON is a raw JSON value (an object, a string — whatever the artifact
// is), placed verbatim under `toolUseResult`.
func ToolUseWithResult(id, name string, input map[string]string, toolUseResultJSON string) (Turn, Turn) {
	// The tool_use, with a top-level `id` as the marker anchor so the block's own
	// `id` stays clean and equal to `id`.
	var ib strings.Builder
	ib.WriteByte('{')
	first := true
	for k, v := range input {
		if !first {
			ib.WriteByte(',')
		}
		first = false
		fmt.Fprintf(&ib, "%q:%s", k, jsonStr(v))
	}
	ib.WriteByte('}')
	use := Turn{jsonl: fmt.Sprintf(
		`{"type":"assistant","id":%q,"uuid":%q,"message":{"role":"assistant","stop_reason":null,"content":[{"type":"tool_use","id":%q,"name":%q,"input":%s}]}}`,
		id+"#u", "e2e-turn-"+id+"u", id, name, ib.String())}
	// The artifact record: a user tool_result for the same id, carrying the
	// `toolUseResult`. Its top-level `id` is the marker anchor; `tool_use_id` stays
	// clean and equal to `id` so it correlates to the tool_use above.
	res := Turn{jsonl: fmt.Sprintf(
		`{"type":"user","id":%q,"uuid":%q,"toolUseResult":%s,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":"the tool produced its result"}]}}`,
		id+"#r", "e2e-turn-"+id+"r", toolUseResultJSON, id)}
	return use, res
}

// CallWithOutput returns the TWO turns of a tool call whose output the mock
// cannot produce itself: the tool_use, and a tool_result for the same id
// carrying output — a background task's receipt, what TaskOutput read back.
//
// The mock answers the tool_use with its own result (executing it, or an error
// for a tool it does not implement); the second turn is a later tool_result for
// the same id, which is the one a test cites. Both records carry a throwaway
// top-level `id` for the turn marker, so the tool_use's own id and the result's
// tool_use_id stay equal to id and correlate — a result whose call is not in
// the record is not tool output.
func CallWithOutput(id, name string, input map[string]string, output string) (Turn, Turn) {
	use, _ := ToolUseWithResult(id, name, input, "null")
	return use, ToolResult(id, output)
}

// AnswerQuestion returns ONE turn carrying an AskUserQuestion ANSWER — the shape a
// person's prompted answer lands as in a real trajectory: a `user` record whose
// content is a `tool_result` block reading
// `The user answered: "<q1>"="<a1>", "<q2>"="<a2>". Read the answers carefully ...`.
//
// # Why this is a real Claude Code shape the mock now emits
//
// An AskUserQuestion answer does NOT arrive as a plain typed message. It re-enters
// the conversation the way EVERY tool result does — as a user-role message carrying
// a tool_result block — and that block's content is the answer envelope, not a
// tool the mock executes locally. a10n-cli#470 taught the mock's scenario validator
// to accept exactly this: a scenario-authored `user` record carrying a tool_result
// WITH a `tool_use_id` is a genuine CC shape and is forwarded + persisted into the
// transcript (record.go validateRecord: "the motivating case is an AskUserQuestion
// answer envelope"). A tool_result with NO tool_use_id stays rejected — that is the
// malformed footgun the old blanket rejection guarded against, and the only thing
// still refused. So a mock-driven run can now carry the answer envelope that used to
// need a hand-authored fixture.
//
// # The record's shape, and why the ids are shaped this way
//
// The turn-firing marker is injected into the FIRST `"id":"…"` of a turn's record
// (see script()/injectMarker). A tool_result-in-user record's own `tool_use_id` is
// NOT written as `"id":"…"`, so without a top-level anchor the marker would find no
// `"id":"` and never be injected — the turn could not tell it had already fired and
// would re-emit every re-run. So the record carries a throwaway top-level `id`
// (`<id>#a`) for the marker to bind to, exactly as ToolUseWithResult does, which
// keeps the block's own `tool_use_id` CLEAN and equal to `id`. The record also
// carries a uuid (keyed off id) so transcript.Read does not skip it as an entry.
//
// # The envelope content
//
// Each qa is {question, answer}. The content is assembled as a PLAIN string with
// literal quotes around each question and answer — that is the envelope's own text —
// joined by `, ` and closed with the `. Read the answers ...` trailer cite's parser
// keys on, then serialized ONCE by jsonStr (which is what turns those quotes into
// the `\"` a JSON string carries). This is byte-for-byte the string the old
// hand-authored multiAnswerEnvelope built; only its delivery moved into the mock.
//
// The turn is NOT terminal and needs no following turn: it is a user record (not a
// tool_use), so the mock forwards it and reaches EOF as end-of-turn — the answer
// envelope lands as the transcript's next record after the seeded prompt.
func AnswerQuestion(id string, qa ...[2]string) Turn {
	content := `The user answered: `
	for i, p := range qa {
		if i > 0 {
			content += `, `
		}
		content += `"` + p[0] + `"="` + p[1] + `"`
	}
	content += `. Read the answers carefully — they may request clarification, changes, or that you not proceed.`
	return Turn{jsonl: fmt.Sprintf(
		`{"type":"user","id":%q,"uuid":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":%s}]}}`,
		id+"#a", "e2e-turn-"+id, id, jsonStr(content))}
}

// ToolResult returns ONE turn carrying a tool's RESULT with arbitrary content — the
// shape a command's output lands as in a real trajectory, and the fixture a
// DELIVERY OBSERVATION is cited against: a `user` record whose content is a
// `tool_result` block whose `content` is the given result text (a test that came
// back green, a build's output).
//
// # Why this is a real Claude Code shape the mock emits
//
// This is the sibling of AnswerQuestion and rests on the exact same mock capability:
// a10n-cli#470 taught the scenario validator that a `user` record carrying a
// tool_result WITH a `tool_use_id` is a genuine CC shape, forwarded and persisted
// into the transcript (record.go validateRecord). AnswerQuestion uses that to carry
// an answer envelope; ToolResult uses it to carry a plain result body. A tool_result
// with NO tool_use_id stays rejected as malformed — the only thing still refused.
//
// The distinction from ToolUseWithResult is the CHANNEL the evidence sits in.
// ToolUseWithResult supplies the entry's top-level `toolUseResult` field — the
// STRUCTURED artifact payload an action-proof check reads whole — and its
// tool_result block content is a fixed placeholder. A delivery OBSERVATION is cited
// with `cite --source-types tool_result`, a SUBSTRING search over the tool_result
// BLOCK body, so the body is exactly what must be controllable here. So this builder
// puts the result text in the block content, which is where cite looks.
//
// # The record's shape, and why the id is shaped this way
//
// Identical to AnswerQuestion's reasoning: the turn-firing marker is injected into
// the FIRST `"id":"…"` of a turn's record, and a tool_result-in-user record's own
// `tool_use_id` is not written as `"id":"…"`. So the record carries a throwaway
// top-level `id` (`<id>#r`) for the marker to bind to, which keeps the block's own
// `tool_use_id` CLEAN and equal to `id`. A uuid (keyed off id) is carried so
// transcript.Read does not skip it as an entry.
//
// The turn is NOT terminal and needs no following turn: it is a user record (not a
// tool_use), so the mock forwards it and reaches EOF as end-of-turn — the result
// lands as the transcript's next record after whatever preceded it.
func ToolResult(id, result string) Turn {
	return Turn{jsonl: fmt.Sprintf(
		`{"type":"user","id":%q,"uuid":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":%s}]}}`,
		id+"#r", "e2e-turn-"+id, id, jsonStr(result))}
}

// Skill returns a turn where the agent loads a skill.
//
// The mock does not implement the Skill tool and answers with an error, which
// is correct for what this is for: the tool_use record lands in the transcript
// either way, and a rule asking whether the agent reached for a skill is asking
// about the reaching. Whether the skill then loaded is the environment's
// business, not evidence about the agent.
func Skill(id, skill string) Turn {
	return Turn{jsonl: toolUse(id, "Skill", map[string]string{"skill": skill})}
}

// Dispatch returns a turn where the agent delegates work to a sub-agent, which
// then runs the scenario at scriptPath in a session of its own.
//
// isolation is "worktree" to give the sub-agent its own tree — the mock binds a
// real `git worktree add` of the project's HEAD, so the sub-agent's hooks run
// with a genuinely separate working directory — and empty to share the
// dispatching session's. The two are the cases a sub-agent's state has to be
// right for, and they differ in exactly one field, which is the point: sharing
// the tree is meant to be the ordinary path, not a second mechanism.
//
// The script field is the mock's own: real Claude Code lets a model decide what
// the sub-agent does, and a test cannot, so the sub-agent's behaviour is
// supplied the same way the dispatching session's is. Every other field is real:
// description, prompt, subagent_type and isolation are all Agent inputs Claude
// Code emits.
//
// The tool is named Agent, which is what Claude Code emits today. Task is the
// tool's former name — it was renamed to Agent in v2.1.63 and Task kept working
// as an accepted alias — so a hook that only matches Task still fires against
// older sessions but never against current ones. A survey of 8,458 local
// transcripts (94,960 tool_use entries) found 493 Agent and zero Task, so a
// harness that emits Task exercises only the legacy alias and would never prove
// a guardrail fires against the name real sessions carry. Consumers should
// accept both names; the harness emits the one production emits.
func Dispatch(id, prompt, scriptPath, isolation string) Turn {
	input := map[string]string{
		"description":   "delegated work",
		"prompt":        prompt,
		"subagent_type": "general-purpose",
		"script":        scriptPath,
	}
	if isolation != "" {
		input["isolation"] = isolation
	}
	return Turn{jsonl: toolUse(id, "Agent", input)}
}

// DispatchNoParent is Dispatch with an EMPTY dispatching tool_use id — the shape a
// sub-agent whose parent is NOT derivable comes from.
//
// # Why the empty id, and what it produces
//
// A sub-agent's meta records the id of the Agent tool_use that dispatched it, and a
// reader derives the immediate parent by finding the trajectory holding a tool_use
// with that id. a10n-claude-mock threads the dispatching tool_use's own id into the
// meta (seedSubagentTranscript's toolUseId), so a normal Dispatch yields a DERIVABLE
// parent. When the tool_use carries NO id, the meta's toolUseId is empty and there is
// nothing to correlate — the honest "parent not derivable" degradation `describe`
// reports as isSubagent=true with parentPath absent. This is the negative case
// T028_03 exercises, and the one shape a normal Dispatch (which always has an id)
// cannot produce.
//
// # Why the id is shaped this way
//
// The turn-firing marker is injected into the FIRST `"id":"…"` of a turn's record
// (script()/injectMarker). If the tool_use block's own `id` were the only one and it
// were empty, the marker would find no `"id":"` to bind to and the turn could not tell
// it had already fired. So the record carries a throwaway TOP-LEVEL `id` (`<id>#np`)
// for the marker to bind to — exactly as ToolUseWithResult/AnswerQuestion do — which
// keeps the tool_use block's own `id` CLEAN and empty. extractFirstToolUseWithID reads
// the BLOCK's id (empty), so the mock threads an empty toolUseId into the meta, while
// the marker rides the top-level anchor. The entry carries a uuid so transcript.Read
// does not skip it.
func DispatchNoParent(id, prompt, scriptPath string) Turn {
	input := map[string]string{
		"description":   "delegated work",
		"prompt":        prompt,
		"subagent_type": "general-purpose",
		"script":        scriptPath,
	}
	var ib strings.Builder
	ib.WriteByte('{')
	first := true
	for k, v := range input {
		if !first {
			ib.WriteByte(',')
		}
		first = false
		fmt.Fprintf(&ib, "%q:%s", k, jsonStr(v))
	}
	ib.WriteByte('}')
	// Top-level `id` is the marker anchor; the tool_use block's own `id` is empty so
	// the mock records an empty toolUseId and the parent stays underivable.
	return Turn{jsonl: fmt.Sprintf(
		`{"type":"assistant","id":%q,"uuid":%q,"message":{"role":"assistant","stop_reason":null,"content":[{"type":"tool_use","id":"","name":"Agent","input":%s}]}}`,
		id+"#np", "e2e-turn-"+id, ib.String())}
}

// Script writes a scenario as a standalone script file and returns its path, for
// handing to Dispatch as what the sub-agent runs.
func (s Scenario) Script(path string) error {
	return os.WriteFile(path, []byte("#!/bin/sh\n"+s.script()+"\n"), 0o755)
}

// script renders the scenario as the shell the mock runs.
//
// Each turn is gated on its own marker being absent from the conversation so
// far, so re-running the script advances rather than repeating. With every turn
// emitted, the scenario finishes.
//
// The marker carries the TURN'S OWN ID, not its index. A bare index is unique
// only within one scenario, and a session that is Run more than once — which is
// how a test settles something and then comes back to it under the same
// conversation — writes its markers into a transcript the next Run reads. With
// `slop-turn-0` already in the file from the first Run, every turn of the
// second looks like it has already fired and the whole scenario emits nothing:
// silently, with no error, and with the test observing an empty second cycle
// that it reads as "the hook did not run". The id is the test's own, so
// distinct scenarios cannot collide unless they deliberately reuse it.
func (s Scenario) script() string {
	var b strings.Builder
	b.WriteString("set -u\nSF=\"${A10N_MOCK_SESSION_FILE:-/dev/null}\"\n")
	b.WriteString("SESS=\"$(cat \"$SF\" 2>/dev/null || true)\"\n")

	for i, t := range s.turns {
		marker := fmt.Sprintf("slop-turn-%d-%s", i, turnID(t.jsonl))
		line := injectMarker(t.jsonl, marker)
		fmt.Fprintf(&b, `if ! printf '%%s' "$SESS" | grep -q %q; then
  printf '%%s\n' %s
  exit 0
fi
`, marker, shQuote(line))
	}
	fmt.Fprintf(&b, `printf '%%s\n' %s`, shQuote(result(s.result)))
	return b.String()
}

// toolUse renders one assistant turn invoking a tool.
//
// The record carries a uuid, keyed off the turn's own id. Without one the line
// is not merely untidy — `transcript.Read` SKIPS every record that has no uuid,
// because Claude Code's own preamble and bookkeeping lines carry none. A turn
// emitted without one therefore never reaches `sr-session query`, so a
// rule reading the trajectory sees an empty session and a test asserting on
// what the agent did passes or fails for reasons that have nothing to do with
// the rule.
//
// The parent is deliberately left off. Nothing this harness drives walks the
// chain — the identity walk reads the seeded ROOT record, which has its own
// uuid and an explicit null parent — and inventing a plausible parent chain
// here would be this file asserting a shape it does not maintain.
func toolUse(id, name string, input map[string]string) string {
	var ib strings.Builder
	ib.WriteByte('{')
	first := true
	for k, v := range input {
		if !first {
			ib.WriteByte(',')
		}
		first = false
		fmt.Fprintf(&ib, "%q:%s", k, jsonStr(v))
	}
	ib.WriteByte('}')
	return fmt.Sprintf(`{"type":"assistant","uuid":%q,"message":{"role":"assistant","stop_reason":null,"content":[{"type":"tool_use","id":%q,"name":%q,"input":%s}]}}`,
		"e2e-turn-"+id, id, name, ib.String())
}

func result(text string) string {
	return fmt.Sprintf(`{"type":"result","subtype":"success","result":%s,"is_error":false}`, jsonStr(text))
}

// turnID reads the tool_use id a turn was built with, before any marker is
// appended to it. Returns "" for a record carrying no id, which leaves the
// marker as the bare index — the old behaviour, and correct for a scenario run
// once.
func turnID(jsonl string) string {
	const idKey = `"id":"`
	i := strings.Index(jsonl, idKey)
	if i < 0 {
		return ""
	}
	j := i + len(idKey)
	end := strings.IndexByte(jsonl[j:], '"')
	if end < 0 {
		return ""
	}
	return jsonl[j : j+end]
}

// injectMarker appends a marker to the tool_use id, so a turn can tell whether
// it has already fired by looking for it in the conversation.
func injectMarker(jsonl, marker string) string {
	const idKey = `"id":"`
	i := strings.Index(jsonl, idKey)
	if i < 0 {
		return jsonl
	}
	j := i + len(idKey)
	end := strings.IndexByte(jsonl[j:], '"')
	if end < 0 {
		return jsonl
	}
	return jsonl[:j] + jsonl[j:j+end] + "-" + marker + jsonl[j+end:]
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func jsonStr(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}

// SayWrite returns ONE assistant turn carrying BOTH a text block (which
// PostTagWrite scans for a #tag) AND a Write tool_use — a tag and a file write
// landing atomically in the same turn.
//
// This exists because a pure-text Say turn is TERMINAL in a10n-claude-mock: an
// assistant message with no tool_use gives the mock nothing to get a result for,
// so it ends the stream, and any turn after a Say never fires. Measured on this
// harness — a scenario `Say(...), Write(...)` emits only the Say. So a rule that
// must see a tag DECLARED BEFORE a subsequent action (a PreToolUse context whose
// enter reads the trajectory for `#refactor`, then a marked write it guards)
// cannot be driven with Say followed by Write. Carrying the tag's prose in the
// SAME block-list message as the tool_use gets the tag into the trajectory
// atomically with the action, and — because the entry has a tool_use id — the
// turn fires once (and is not terminal, there is a result to wait for) exactly
// like any other tool turn.
//
// The record is a `type:"assistant"` block-list message `[{text},{tool_use}]`,
// the shape Claude Code writes for a turn that both says something and calls a
// tool. transcript.AssistantText reads the text block (so PostTagWrite sees the
// tag) and transcript.ToolCalls reads the tool_use (so the file event fires).
func SayWrite(id, prose, path, content string) Turn {
	return Turn{jsonl: sayWithTool(id, prose, "Write", map[string]string{
		"file_path": path,
		"content":   content,
	})}
}

// SayBash is SayWrite's Bash sibling: one assistant turn carrying a text block
// (scanned for a #tag) and a Bash tool_use, for a tag declared atomically with a
// shell command — needed for the same terminal-Say reason SayWrite documents.
func SayBash(id, prose, command string) Turn {
	return Turn{jsonl: sayWithTool(id, prose, "Bash", map[string]string{"command": command})}
}

// BashBatch returns ONE assistant turn whose content is SEVERAL Bash tool_use
// blocks — the multi-tool-call-in-one-entry shape a spread-yield rests on, where a
// single assistant entry carries more than one tool call and a per-entry derivation
// must yield one event per call.
//
// Real Claude Code emits several tool calls in a single assistant message, and the
// mock forwards that entry VERBATIM: scanLines breaks the turn loop at the FIRST
// tool_use to execute it and synthesise its result, but the entry it forwarded and
// persisted still carries ALL the blocks (measured — a three-Bash entry reads back
// as one entry yielding three PreCommandInvoke). That is why the ordinary scenario
// API — one tool call per Turn — could not build this: three Bash turns are three
// separate entries with one event each, not the one entry with three this is for.
//
// The FIRST block carries the turn's id, which is where the marker binds (script()
// injects it into the first `"id":"…"`), so the turn fires once; the remaining
// blocks carry `<id>-N` ids, clean and distinct. The entry carries a uuid keyed off
// id so transcript.Read does not skip it. Only the first tool_use is executed by the
// mock (its synthesised result correlates by the first id); the later blocks are
// trajectory records the derivation reads, which is all a spread-yield needs.
func BashBatch(id string, commands ...string) Turn {
	blocks := make([]string, 0, len(commands))
	for i, cmd := range commands {
		blockID := id
		if i > 0 {
			blockID = fmt.Sprintf("%s-%d", id, i)
		}
		blocks = append(blocks, fmt.Sprintf(
			`{"type":"tool_use","id":%q,"name":"Bash","input":{"command":%s}}`,
			blockID, jsonStr(cmd)))
	}
	return Turn{jsonl: fmt.Sprintf(
		`{"type":"assistant","uuid":%q,"message":{"role":"assistant","stop_reason":null,"content":[%s]}}`,
		"e2e-turn-"+id, strings.Join(blocks, ","))}
}

// sayWithTool renders one assistant turn whose content is a two-block list: a
// text block, then a tool_use. The tool_use carries the turn's id (so the mock's
// marker machinery fires it once) and the entry a uuid (so transcript.Read does
// not skip it), the same invariants toolUse and Say each keep for their single
// block.
func sayWithTool(id, prose, name string, input map[string]string) string {
	var ib strings.Builder
	ib.WriteByte('{')
	first := true
	for k, v := range input {
		if !first {
			ib.WriteByte(',')
		}
		first = false
		fmt.Fprintf(&ib, "%q:%s", k, jsonStr(v))
	}
	ib.WriteByte('}')
	return fmt.Sprintf(
		`{"type":"assistant","uuid":%q,"message":{"role":"assistant","stop_reason":null,"content":[{"type":"text","text":%s},{"type":"tool_use","id":%q,"name":%q,"input":%s}]}}`,
		"e2e-turn-"+id, jsonStr(prose), id, name, ib.String())
}
