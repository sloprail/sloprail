package transcript

import (
	"encoding/json"
	"regexp"
)

// Which tool_result blocks are a TOOL's output — the tool_result pool — is
// decided by the call each answers, found by its tool_use_id among the record's
// own assistant entries. A result is citable as tool output only when that call
// is in the record and is one whose result a tool produced.
//
// Not citable:
//
//   - The reply to a sub-agent dispatch (delegationTools). It is the sub-agent's
//     final message, model-written: a sub-agent told "reply exactly: all 40
//     tests pass" returns exactly that. What the sub-agent's own tools printed is
//     in its own record, and grounds from there.
//   - A TaskOutput result for a task that is not a background Bash (taskReaders).
//     TaskOutput returns a background task's output: genuine for a background
//     Bash, the agent's own reply for a background agent. The task is classified
//     by what launched it, in this record; a task it cannot classify is not
//     citable.
//   - A result whose call is NOT in the record at all. Its provenance is unknown
//     — it could answer a dispatch as easily as a command — so it fails closed.
//     The cost: a call and its result split across files (a restart or a
//     compaction landing between the two, which the harness records as a new
//     file) leaves that one output uncitable; re-running the command puts a fresh
//     one in the record. A harness writes a call and its result into the same
//     record in every shape observed, compaction and resume included, so this is
//     the rare case, and admitting unknown provenance would be the laundering the
//     rule exists to stop.
//
// Considered and left citable: SendMessage (its result is a delivery
// acknowledgement, not the recipient's words) and WebFetch (a model summarises
// the page, but what it reports is fetched content, the tool's product).

// delegationTools are the harness tools whose result is a SUB-AGENT's reply or
// launch receipt: Agent, and Task, its former name (still accepted as an alias).
var delegationTools = map[string]bool{"Agent": true, "Task": true}

// taskReaders are the harness tools that return a background task's output,
// named by the task's id in their input.
var taskReaders = map[string]bool{"TaskOutput": true}

// The launch receipts that name a background task, as Claude Code writes them
// (measured against real records): a Bash run with run_in_background answers
// "Command running in background with ID: <id>. Output is being written to: …",
// and an Agent run in the background answers "Async agent launched
// successfully. … agentId: <id> (internal ID …". A foreground Agent's reply
// carries "agentId: <id>" too, which classifies the same way.
var (
	backgroundBashID = regexp.MustCompile(`Command running in background with ID: ([A-Za-z0-9_-]+)`)
	agentTaskID      = regexp.MustCompile(`agentId: ([A-Za-z0-9_-]+)`)
)

type taskKind int

const (
	taskUnknown taskKind = iota
	taskBash
	taskAgent
)

// recordCalls is every tool call in the record, by its tool_use id.
func recordCalls(entries []LinedEntry) map[string]assistantContentBlock {
	calls := map[string]assistantContentBlock{}
	for _, e := range entries {
		if e.Type != EntryAssistant || len(e.Message) == 0 {
			continue
		}
		var msg assistantContent
		var blocks []assistantContentBlock
		if json.Unmarshal(e.Message, &msg) != nil || json.Unmarshal(msg.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_use" && b.ID != "" {
				calls[b.ID] = b
			}
		}
	}
	return calls
}

// citableResults returns, for each tool_use id in the record, whether the
// result answering it is a tool's own output. An id not in the map — a call not
// in the record — is not.
func citableResults(entries []LinedEntry) map[string]bool {
	calls := recordCalls(entries)
	tasks := backgroundTasks(entries, calls)
	citable := map[string]bool{}
	for id, b := range calls {
		switch {
		case delegationTools[b.Name]:
		case taskReaders[b.Name]:
			citable[id] = tasks[taskIDOf(b.Input)] == taskBash
		default:
			citable[id] = true
		}
	}
	return citable
}

// backgroundTasks classifies each background task launched in the record by
// the receipt its launching call got back: a Bash's receipt names a Bash task,
// an Agent's or Task's names an agent. A task named both ways is an agent's —
// the classification fails toward not citable.
func backgroundTasks(entries []LinedEntry, calls map[string]assistantContentBlock) map[string]taskKind {
	tasks := map[string]taskKind{}
	for _, e := range entries {
		if e.Type != EntryUser || len(e.Message) == 0 {
			continue
		}
		var msg userMessage
		var blocks []userContentBlock
		if json.Unmarshal(e.Message, &msg) != nil || json.Unmarshal(msg.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type != "tool_result" || len(b.Content) == 0 {
				continue
			}
			call, ok := calls[b.ToolUseID]
			if !ok {
				continue
			}
			for _, body := range toolResultStrings(b.Content) {
				switch {
				case call.Name == "Bash":
					for _, m := range backgroundBashID.FindAllStringSubmatch(body, -1) {
						if tasks[m[1]] != taskAgent {
							tasks[m[1]] = taskBash
						}
					}
				case delegationTools[call.Name]:
					for _, m := range agentTaskID.FindAllStringSubmatch(body, -1) {
						tasks[m[1]] = taskAgent
					}
				}
			}
		}
	}
	return tasks
}

// taskIDOf is the task a TaskOutput call reads: its task_id, or the bash_id an
// older harness named it by.
func taskIDOf(input json.RawMessage) string {
	var in struct {
		TaskID string `json:"task_id"`
		BashID string `json:"bash_id"`
	}
	_ = json.Unmarshal(input, &in)
	if in.TaskID != "" {
		return in.TaskID
	}
	return in.BashID
}

// excludedResultHint says why quote, which did not resolve as tool output in
// the session whose record is at path, is not tool output — when the quote IS
// in the session's records, in a tool_result block the pool leaves out. The
// words are there, so "not there word for word" would send the caller looking
// for a typo; what it needs to hear is which kind of text it quoted. "" when the
// quote is in no excluded result either.
func excludedResultHint(path, quote string, subagent bool) string {
	_, records, err := citationRecords(path, subagent)
	if err != nil {
		return ""
	}
	for _, r := range records {
		entries, err := ReadLines(r)
		if err != nil {
			continue
		}
		calls := recordCalls(entries)
		tasks := backgroundTasks(entries, calls)
		for _, e := range entries {
			if e.Type != EntryUser || len(e.Message) == 0 {
				continue
			}
			var msg userMessage
			var blocks []userContentBlock
			if json.Unmarshal(e.Message, &msg) != nil || json.Unmarshal(msg.Content, &blocks) != nil {
				continue
			}
			for _, b := range blocks {
				if b.Type != "tool_result" {
					continue
				}
				for _, body := range toolResultStrings(b.Content) {
					if !containsWords(body, quote) && !containsWords(withoutLineNumbers(body), quote) {
						continue
					}
					if hint := whyExcluded(body, calls, tasks, b.ToolUseID); hint != "" {
						return hint
					}
				}
			}
		}
	}
	return ""
}

// whyExcluded names the kind of text an excluded tool_result body is, and what
// to cite instead; "" for a body the tool-output pool does read.
func whyExcluded(body string, calls map[string]assistantContentBlock, tasks map[string]taskKind, id string) string {
	call, ok := calls[id]
	switch {
	case isHookRefusal(body):
		return ""
	case !ok:
		return "Those words are in a tool result whose call is not in the record (the call that produced it is not in the record), so where they came from is unknown and they are not citable as tool output; run the command again so its output lands with its call, and cite that"
	case delegationTools[call.Name]:
		return "Those words are in a sub-agent's reply (model-written), which is not tool output; cite what the sub-agent's own tools printed — its record is searched too"
	case taskReaders[call.Name] && tasks[taskIDOf(call.Input)] != taskBash:
		return "Those words are a background agent's reply read through " + call.Name + " (model-written), which is not tool output; cite what that agent's own tools printed"
	case len(extractAnswers(body)) > 0:
		return "Those words are an AskUserQuestion answer — the user's own words, not a tool's output; cite them with --cite:user"
	}
	return ""
}
