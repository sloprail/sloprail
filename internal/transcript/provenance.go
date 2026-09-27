package transcript

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
//   - A TaskOutput result. TaskOutput returned a background task's output — a
//     background agent's reply as readily as a command's — and no harness
//     version this was measured against (Claude Code 2.1.170–2.1.282) calls it
//     any more; a background task's output is a file now (below). It is kept
//     uncitable, not classified, so a legacy record cannot launder an agent's
//     reply through it.
//   - A result of a call that READ AN AGENT TRANSCRIPT (readsTranscript): a Read,
//     a Grep or a command whose target is a Claude Code record — any .jsonl
//     under the harness's projects directory (a sub-agent's record, the
//     session's own), or a file that reads as one, including a background
//     agent's `tasks/<id>.output`, which is a symbolic link to that agent's
//     record. Its text is model-written; read back through a tool it would
//     otherwise ground as the tool's output.
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

// taskReaders are the harness tools that returned a background task's output.
var taskReaders = map[string]bool{"TaskOutput": true}

// recordCall is a tool call in the record and the directory it ran in.
type recordCall struct {
	assistantContentBlock
	cwd string
}

// recordCalls is every tool call in the record, by its tool_use id.
func recordCalls(entries []LinedEntry) map[string]recordCall {
	calls := map[string]recordCall{}
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
				calls[b.ID] = recordCall{b, e.Cwd}
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
	citable := map[string]bool{}
	for id, c := range calls {
		citable[id] = !delegationTools[c.Name] && !taskReaders[c.Name] && !readsTranscript(c)
	}
	return citable
}

// readsTranscript reports whether a call's target is an agent's transcript: a
// path in its input (file_path, path, notebook_path) or, for a shell command,
// any word of it, that resolves — through symbolic links — to a Claude Code
// record or into the harness's projects directory; or a command that names
// that directory at all (a glob over it cannot be resolved ahead of time).
func readsTranscript(c recordCall) bool {
	var in struct {
		FilePath     string `json:"file_path"`
		Path         string `json:"path"`
		NotebookPath string `json:"notebook_path"`
		Command      string `json:"command"`
	}
	_ = json.Unmarshal(c.Input, &in)
	candidates := []string{in.FilePath, in.Path, in.NotebookPath}
	if in.Command != "" {
		if strings.Contains(in.Command, ".claude/projects") {
			return true
		}
		for _, w := range strings.Fields(in.Command) {
			candidates = append(candidates, strings.Trim(w, `'"();|&<>`))
		}
	}
	for _, p := range candidates {
		if p != "" && isTranscriptPath(p, c.cwd) {
			return true
		}
	}
	return false
}

// isTranscriptPath reports whether path, as a call in cwd would read it, is an
// agent's transcript or lies in the harness's projects directory.
func isTranscriptPath(path, cwd string) bool {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	if !filepath.IsAbs(path) {
		if cwd == "" {
			return false
		}
		path = filepath.Join(cwd, path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	if projects := ConfigDir(); projects != "" {
		if root, err := filepath.EvalSymlinks(filepath.Join(projects, "projects")); err == nil {
			if real == root || strings.HasPrefix(real, root+string(filepath.Separator)) {
				return true
			}
		}
	}
	return readsAsTranscript(real)
}

// readsAsTranscript reports whether the file at path is a harness record: its
// first lines are records carrying a uuid, a conversational type and a message.
// A copy of a transcript moved out of the projects directory is still one.
func readsAsTranscript(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for i := 0; i < 20 && sc.Scan(); i++ {
		var rec struct {
			UUID    string          `json:"uuid"`
			Type    string          `json:"type"`
			Message json.RawMessage `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue
		}
		if rec.UUID != "" && (rec.Type == "user" || rec.Type == "assistant") && len(rec.Message) > 0 {
			return true
		}
	}
	return false
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
					if hint := whyExcluded(body, calls, b.ToolUseID); hint != "" {
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
func whyExcluded(body string, calls map[string]recordCall, id string) string {
	call, ok := calls[id]
	switch {
	case isHookRefusal(body):
		return ""
	case !ok:
		return "Those words are in a tool result whose call is not in the record (the call that produced it is not in the record), so where they came from is unknown and they are not citable as tool output; run the command again so its output lands with its call, and cite that"
	case delegationTools[call.Name]:
		return "Those words are in a sub-agent's reply (model-written), which is not tool output; cite what the sub-agent's own tools printed — its record is searched too"
	case taskReaders[call.Name]:
		return "Those words are what " + call.Name + " returned — a background task's output, which may be an agent's model-written reply — so they are not citable as tool output; cite what that task's own tools printed"
	case readsTranscript(call):
		return "Those words were read out of an agent transcript (the file is an agent's record — its text is model-written), which is not tool output; cite what the tools in that record printed — sub-agents' records are searched too"
	case len(extractAnswers(body)) > 0:
		return "Those words are an AskUserQuestion answer — the user's own words, not a tool's output; cite them with --cite:user"
	}
	return ""
}
