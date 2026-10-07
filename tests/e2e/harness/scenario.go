package harness

import (
	"fmt"
	"os"
	"strings"
	"testing"
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
	// act is what the agent does, in no harness's words: each Driver renders it.
	act Action
	// launchedOutput marks a turn whose jsonl names the output file of the most
	// recently launched background command as @@LAUNCHED_OUTPUT@@, filled in when
	// the turn is emitted — the mock picks the file at launch, and only its
	// receipt says where it is, as it does for a real agent.
	launchedOutput bool
	// launchedTask marks a turn whose jsonl names the id of the most recently
	// launched background task as @@LAUNCHED_TASK@@, filled in when the turn is
	// emitted — the id is minted by the mock at launch (a Bash's own id, or an
	// Agent's agentId) and only its receipt says what it is.
	launchedTask bool
}

// launchedOutputPlaceholder is replaced by the output file of the most recently
// launched background command when a launchedOutput turn is emitted.
const launchedOutputPlaceholder = "@@LAUNCHED_OUTPUT@@"

// launchedTaskPlaceholder is replaced by the id of the most recently launched
// background task when a launchedTask turn is emitted.
const launchedTaskPlaceholder = "@@LAUNCHED_TASK@@"

// Turns builds a scenario ending in the given assistant text.
func Turns(finalText string, turns ...Turn) Scenario {
	return Scenario{turns: turns, result: finalText}
}

// Write returns a turn where the agent writes a file.
func Write(id, path, content string) Turn {
	return Turn{act: Action{Kind: ActWrite, ID: id, Path: path, Content: content}}
}

// Edit returns a turn where the agent replaces text in a file.
func Edit(id, path, oldString, newString string) Turn {
	return Turn{act: Action{Kind: ActEdit, ID: id, Path: path, Old: oldString, New: newString}}
}

// Bash returns a turn where the agent runs a shell command.
func Bash(id, command string) Turn {
	return Turn{act: Action{Kind: ActBash, ID: id, Command: command}}
}

// WebFetch returns a turn where the agent fetches a page: the real tool's url and
// prompt, plus the mock's own mock_result, the page text the scripted call is
// answered with (the mock reaches no web). The mock refuses a url that is not an
// http or https address with a host, so a malformed one cannot be sent here.
func WebFetch(id, url, prompt string) Turn {
	return Turn{act: Action{Kind: ActWebFetch, ID: id, Text: url, Text2: prompt}}
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
	return Turn{act: Action{Kind: ActSay, ID: id, Text: text}}
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
// check reading the input as JSON (`.input.email`) reads them as such. (For the
// tools the mock models, inputs real Claude Code types as booleans or numbers are
// written typed: see typedInputs.)
//
// The mock's own synthesised result for the tool carries no `toolUseResult`, so a
// tool WITH a produced artifact a check reads back — a screenshot whose image an
// audit inspects — uses ToolUseWithResult instead, which supplies that field.
func ToolUse(id, name string, input map[string]string) Turn {
	return Turn{act: Action{Kind: ActToolUse, ID: id, Tool: name, Input: input}}
}

// ToolUseJSON is ToolUse with the input given as a raw JSON object, for a tool
// whose input carries numbers, nested objects or arrays — values a check must
// carry through as JSON, which a map of strings cannot express.
func ToolUseJSON(id, name, inputJSON string) Turn {
	return Turn{act: Action{Kind: ActToolUseJSON, ID: id, Tool: name, Raw: inputJSON}}
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
	use, res := toolUseWithResultRaw(id, name, "", toolUseResultJSON)
	use.act.Input = input
	return use, res
}

// toolUseWithResultRaw is ToolUseWithResult with the input already a JSON object.
func toolUseWithResultRaw(id, name, inputJSON, toolUseResultJSON string) (Turn, Turn) {
	return Turn{act: Action{Kind: ActCall, ID: id, Tool: name, Raw: inputJSON}},
		Turn{act: Action{Kind: ActArtifact, ID: id, Raw: toolUseResultJSON}}
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
	return Turn{act: Action{Kind: ActAnswer, ID: id, QA: qa}}
}

// AskUserQuestion returns the TWO turns of a question the agent asks and the
// person answers, as a real record holds them: the AskUserQuestion tool_use,
// then the answer envelope (AnswerQuestion) as the tool_result for that same
// call. The call matters: a result whose call is not in the record is of
// unknown provenance and is dropped from the tool-output pool for THAT reason,
// so an answer without its question cannot show that an answer is kept out of
// the tool-output pool because it is the user's words.
//
// The tool_use carries the input real Claude Code sends (a questions array with
// options), which the mock models; it has no user to ask, so it answers the call
// with its own error result, and the answer envelope follows it for the same id,
// the way the harness writes the person's selection. The harness runs the mock
// with a permission host (--permission-prompt-tool stdio), which is what offers
// AskUserQuestion to a non-interactive run.
func AskUserQuestion(id, question, answer string) (Turn, Turn) {
	input := fmt.Sprintf(`{"questions":[{"question":%s,"header":"Question","multiSelect":false,"options":[{"label":%s,"description":%s},{"label":"other","description":"another answer"}]}]}`,
		jsonStr(question), jsonStr(answer), jsonStr(answer))
	use, _ := toolUseWithResultRaw(id, "AskUserQuestion", input, "null")
	return use, AnswerQuestion(id, [2]string{question, answer})
}

// AnswerIfAsked is AnswerQuestion where the harness has an answer record (CapAskUserQuestion)
// and NO turn where it has none. Codex and Cursor offer the agent no question tool, so
// nothing the person "answered" can be in their record: a test that pins what the product
// does with an answer envelope runs the same scenario and asserts, on those harnesses, what
// the product does when the record holds no such answer — the explicit counterpart, the
// words never having been said.
func AnswerIfAsked(t testing.TB, id string, qa ...[2]string) []Turn {
	t.Helper()
	if !HasCap(t, CapAskUserQuestion) {
		return nil
	}
	return []Turn{AnswerQuestion(id, qa...)}
}

// AskUserQuestionIfOffered is AskUserQuestion's two turns where the harness offers the
// question tool (CapAskUserQuestion), and none where it does not (see AnswerIfAsked).
func AskUserQuestionIfOffered(t testing.TB, id, question, answer string) []Turn {
	t.Helper()
	if !HasCap(t, CapAskUserQuestion) {
		return nil
	}
	ask, ans := AskUserQuestion(id, question, answer)
	return []Turn{ask, ans}
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
	return Turn{act: Action{Kind: ActToolResult, ID: id, Text: result}}
}

// Skill returns a turn where the agent loads a skill.
//
// The mock does not implement the Skill tool and answers with an error, which
// is correct for what this is for: the tool_use record lands in the transcript
// either way, and a rule asking whether the agent reached for a skill is asking
// about the reaching. Whether the skill then loaded is the environment's
// business, not evidence about the agent.
func Skill(id, skill string) Turn {
	return Turn{act: Action{Kind: ActSkill, ID: id, Text: skill}}
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
	return Turn{act: Action{Kind: ActDispatch, ID: id, Text: prompt, Script: scriptPath, Isolation: isolation}}
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
	return Turn{act: Action{Kind: ActDispatchNoParent, ID: id, Text: prompt, Script: scriptPath}}
}

// Script writes a scenario as a standalone script file and returns its path, for
// handing to Dispatch as what the sub-agent runs.
func (s Scenario) Script(path string) error {
	script, err := s.script()
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755)
}

// script renders the scenario as the shell the agent runs, in the selected
// harness's own dialect (Driver.RenderScript).
func (s Scenario) script() (string, error) { return mustDriver().RenderScript(s) }

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
	return Turn{act: Action{Kind: ActSayWrite, ID: id, Text: prose, Path: path, Content: content}}
}

// SayBash is SayWrite's Bash sibling: one assistant turn carrying a text block
// (scanned for a #tag) and a Bash tool_use, for a tag declared atomically with a
// shell command — needed for the same terminal-Say reason SayWrite documents.
func SayBash(id, prose, command string) Turn {
	return Turn{act: Action{Kind: ActSayBash, ID: id, Text: prose, Command: command}}
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
	return Turn{act: Action{Kind: ActBashBatch, ID: id, Commands: commands}}
}

// Compact returns ONE turn in which the harness compacts the context, the way
// real Claude Code does: a compact_boundary record appended to the transcript —
// parentless, naming the last record before it as its logicalParentUuid — then
// the summary chained to it, then SessionStart with source "compact"
// (a10n-claude-mock's {"type":"compact"} control record).
func Compact(id string) Turn {
	return Turn{act: Action{Kind: ActCompact, ID: id}}
}

// CompactNamingUnwrittenParent is Compact with a boundary whose logical parent
// is a record written to no transcript — the shape a real preserved-segment
// compaction left, where the only trace of what it continues is the boundary
// record itself, part-way down the file the compaction happened in.
func CompactNamingUnwrittenParent(id string) Turn {
	return Turn{act: Action{Kind: ActCompact, ID: id, UnwrittenParent: true}}
}

// Background launches a tool call in the background — a Bash or an Agent with
// run_in_background — which the mock answers the way real Claude Code does: at
// once, with a receipt naming the task ("Command running in background with
// ID: …" / "Async agent launched successfully. … agentId: …"), and later with a
// <task-notification> when the task finishes. The harness gives every run its
// own CLAUDE_CODE_TMPDIR, so the task's output file is inside the test's
// sandbox, not the shared /tmp.
func Background(id, name string, input map[string]string) Turn {
	in := map[string]string{"run_in_background": "true"} // written as the JSON boolean, see typedInputs
	for k, v := range input {
		in[k] = v
	}
	return Turn{act: Action{Kind: ActToolUse, ID: id, Tool: name, Input: in, Background: true}}
}

// ReadLaunchedOutput reads the output file of the most recently launched
// background command with the Read tool — the way a real agent gets a
// background command's output: its receipt says "To check interim output, use
// Read on that file path", and the <task-notification> that follows carries
// only a summary, never the output. (The mock no longer implements TaskOutput
// as a tool: real transcripts hold no call to it.)
func ReadLaunchedOutput(id string) Turn {
	return Turn{act: Action{Kind: ActToolUse, ID: id, Tool: "Read", Input: map[string]string{"file_path": launchedOutputPlaceholder}}, launchedOutput: true}
}

// ToolResultWithText returns ONE turn whose `user` record carries a tool_result
// block AND a text block: an entry that is not purely a tool's output, so it still
// holds words the person typed. The sibling of ToolResult for a check that must
// treat a mixed entry differently from a pure tool_result one.
func ToolResultWithText(id, result, text string) Turn {
	return Turn{act: Action{Kind: ActToolResultWithText, ID: id, Text: result, Text2: text}}
}
