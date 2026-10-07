// Package record is Codex's session file ("rollout") format: how one JSONL line is
// read into the neutral harness.Record, and where Codex keeps those files. It
// imports only the neutral harness package, like claudecode/record.
//
// # The rollout format
//
// A rollout is `<CODEX_HOME>/sessions/YYYY/MM/DD/rollout-<timestamp>-<session id>.jsonl`,
// one object per line: {"timestamp", "ordinal", "type", "payload"}. The types sloprail
// reads (recorded: harness-mocks codex-mock/snapshots/runs/*/transcript):
//
//	session_meta                         the session's id, cwd, (fork) forked_from_id
//	turn_context                         per-turn cwd and model
//	response_item / message              role user | assistant | developer
//	response_item / function_call        a tool call: shell, shell_command, exec_command, apply_patch
//	response_item / custom_tool_call     a tool call: apply_patch (input is the patch) or
//	                                     exec (input is a JavaScript program calling tools.*)
//	response_item / *_output             the call's result, by call_id
//	event_msg / item_completed           HookPrompt: what a hook fed the agent
//	everything else                      bookkeeping, passed on as an id-less system record
//
// # Normalisation
//
// internal/transcript reads Claude Code's message shapes (assistant content blocks,
// tool_use / tool_result). ParseRecord therefore re-spells each Codex item in that
// vocabulary — a user turn is `type: user` with string content, a tool call an
// assistant `tool_use` block under the canonical tool name (Bash, apply_patch), its
// result a user `tool_result` block — so everything above this package (citations,
// trajectory normalisation, identity) stays harness-neutral. What cannot be
// recovered (a JavaScript `exec` program that is not a single recognised tools.*
// call) keeps the tool name "exec" and is no write and no command to any module.
//
// # Identity
//
// The first session_meta line is the conversation's root: UUID is the session id and
// the parent is absent. A fork is a rollout of its own (its own thread id) whose
// session_meta names the thread it branched from (forked_from_id; recorded: harness-mocks
// codex-mock session-fork, which holds none of the ancestor's lines): that is the record
// the fork continues, so the identity walk crosses into the ancestor's rollout and the
// whole family resolves to one identity. Every other line names a (non-nil) parent,
// which is all the identity walk asks of it.
package record

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/sloprail/sloprail/internal/harness"
)

// Transcripts is Codex's harness.Transcripts.
type Transcripts struct{}

type line struct {
	Timestamp string          `json:"timestamp"`
	Ordinal   int             `json:"ordinal"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type payload struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	SessionID string          `json:"session_id"`
	Cwd       string          `json:"cwd"`
	Role      string          `json:"role"`
	Name      string          `json:"name"`
	CallID    string          `json:"call_id"`
	Arguments string          `json:"arguments"`
	Input     json.RawMessage `json:"input"`
	Output    json.RawMessage `json:"output"`
	Content   json.RawMessage `json:"content"`
	Item      json.RawMessage `json:"item"`

	// ThreadSource is "subagent" on a sub-agent's session_meta, "user" on a root's.
	ThreadSource string `json:"thread_source"`
}

// ParseRecord parses one line of a Codex rollout.
func (Transcripts) ParseRecord(raw []byte) (harness.Record, error) {
	var l line
	if err := json.Unmarshal(raw, &l); err != nil {
		return harness.Record{}, err
	}
	if l.Type == "" {
		return harness.Record{}, fmt.Errorf("not a rollout line: no type")
	}
	var p payload
	_ = json.Unmarshal(l.Payload, &p)

	notFirst := ""
	parent := &notFirst
	rec := harness.Record{Type: "system", ParentUUID: parent, Timestamp: l.Timestamp}

	switch l.Type {
	case "session_meta":
		// The thread's OWN id: a sub-agent's rollout names the root session in session_id
		// (recorded: harness-mocks codex-mock nested-subagents), so keying on that would
		// give the sub-agent its parent's identity and state.
		id := firstNonEmpty(p.ID, p.SessionID)
		rec.UUID, rec.ParentUUID, rec.SessionID, rec.Cwd = id, nil, id, p.Cwd
		rec.IsSidechain = p.ThreadSource == "subagent"
	case "turn_context":
		rec.Cwd = p.Cwd
	case "response_item":
		rec = responseItem(rec, l, p)
	case "event_msg":
		rec = eventMsg(rec, l, p)
	}
	if l.Type == "session_meta" {
		rec.LogicalParentUUID = forkedFrom(l.Payload)
	}
	return rec, nil
}

// forkedFrom is the thread a fork's session_meta says it branched from (forked_from_id), nil
// for any other thread. A fork is a rollout of its own that continues the conversation of
// that thread, whose session_meta opens on that id (recorded: harness-mocks codex-mock
// session-fork).
func forkedFrom(meta json.RawMessage) *string {
	var m struct {
		ForkedFromID string `json:"forked_from_id"`
	}
	if json.Unmarshal(meta, &m) != nil || m.ForkedFromID == "" {
		return nil
	}
	return &m.ForkedFromID
}

func eventMsg(rec harness.Record, l line, p payload) harness.Record {
	if p.Type != "item_completed" {
		return rec
	}
	var item struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		Fragments []struct {
			Text string `json:"text"`
		} `json:"fragments"`
	}
	if json.Unmarshal(p.Item, &item) != nil || item.Type != "HookPrompt" {
		return rec
	}
	// What a hook fed the agent (a Stop hook's refusal, say): a user turn the
	// harness wrote, which is what Claude Code's isMeta means.
	var texts []string
	for _, f := range item.Fragments {
		texts = append(texts, f.Text)
	}
	rec.Type, rec.IsMeta, rec.UUID = "user", true, firstNonEmpty(item.ID, ordinalID(l))
	rec.Message = message("user", strings.Join(texts, "\n"))
	return rec
}

func responseItem(rec harness.Record, l line, p payload) harness.Record {
	rec.UUID = firstNonEmpty(p.ID, p.CallID, ordinalID(l))
	switch p.Type {
	case "message":
		text := inputText(p.Content)
		switch p.Role {
		case "user":
			rec.Type, rec.Message = "user", message("user", text)
			rec.IsMeta = harnessPreamble(text)
		case "assistant":
			rec.Type, rec.Message = "assistant", blocks("assistant", textBlock(text))
		}
	case "function_call", "custom_tool_call":
		name, args := argumentsOf(p)
		rec.Type, rec.Message = "assistant", blocks("assistant", toolUse(p.CallID, name, args))
	case "function_call_output", "custom_tool_call_output":
		rec.Type = "user"
		rec.Message = blocks("user", map[string]any{
			"type": "tool_result", "tool_use_id": p.CallID, "content": inputText(p.Output),
		})
	}
	return rec
}

// harnessPreamble reports a user-role item Codex wrote itself (the environment
// snapshot, AGENTS.md instructions) rather than the person.
func harnessPreamble(text string) bool {
	t := strings.TrimSpace(text)
	return strings.HasPrefix(t, "<environment_context>") ||
		strings.HasPrefix(t, "<user_instructions>") ||
		strings.HasPrefix(t, "# AGENTS.md instructions") ||
		strings.HasPrefix(t, "<turn_aborted>")
}

// argumentsOf maps a Codex tool call onto the canonical (name, arguments).
func argumentsOf(p payload) (name string, args map[string]any) {
	switch p.Name {
	case "shell":
		var a struct {
			Command []string `json:"command"`
		}
		if json.Unmarshal([]byte(p.Arguments), &a) == nil && len(a.Command) > 0 {
			return "Bash", map[string]any{"command": shellLine(a.Command)}
		}
	case "shell_command":
		var a struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(p.Arguments), &a) == nil && a.Command != "" {
			return "Bash", map[string]any{"command": a.Command}
		}
	case "exec_command":
		var a struct {
			Cmd string `json:"cmd"`
		}
		if json.Unmarshal([]byte(p.Arguments), &a) == nil && a.Cmd != "" {
			return "Bash", map[string]any{"command": a.Cmd}
		}
	case "apply_patch":
		if patch := patchOf(p); patch != "" {
			return "apply_patch", map[string]any{"command": patch}
		}
	case "exec":
		var js string
		if json.Unmarshal(p.Input, &js) == nil {
			if n, a, ok := programCall(js); ok {
				return n, a
			}
			return "exec", map[string]any{"code": js}
		}
	}
	return p.Name, map[string]any{"arguments": p.Arguments}
}

func patchOf(p payload) string {
	var s string
	if json.Unmarshal(p.Input, &s) == nil && s != "" {
		return s
	}
	var a struct {
		Input string `json:"input"`
	}
	if json.Unmarshal([]byte(p.Arguments), &a) == nil {
		return a.Input
	}
	return ""
}

// shellLine is the command line of a ["bash", "-lc", "<line>"] argv, else the argv.
func shellLine(argv []string) string {
	if len(argv) >= 3 && (argv[1] == "-lc" || argv[1] == "-c") {
		return argv[2]
	}
	return strings.Join(argv, " ")
}

var (
	execCommandCall = regexp.MustCompile(`tools\.exec_command\(\{\s*cmd:\s*("(?:[^"\\]|\\.)*")`)
	applyPatchCall  = regexp.MustCompile(`tools\.apply_patch\(\s*("(?:[^"\\]|\\.)*"|[A-Za-z_]\w*)\s*\)`)
	constString     = regexp.MustCompile(`const\s+(\w+)\s*=\s*("(?:[^"\\]|\\.)*")`)
)

// programCall recognises the two single-call JavaScript programs Codex's exec tool
// is recorded running: tools.exec_command({cmd: "<line>", ...}) and
// tools.apply_patch("<patch>") (directly or through a const). Anything else is not
// recognised and stays an opaque "exec".
func programCall(js string) (string, map[string]any, bool) {
	if m := applyPatchCall.FindStringSubmatch(js); m != nil {
		lit := m[1]
		if !strings.HasPrefix(lit, `"`) {
			for _, c := range constString.FindAllStringSubmatch(js, -1) {
				if c[1] == lit {
					lit = c[2]
				}
			}
		}
		var patch string
		if json.Unmarshal([]byte(lit), &patch) == nil && patch != "" {
			return "apply_patch", map[string]any{"command": patch}, true
		}
	}
	if m := execCommandCall.FindStringSubmatch(js); m != nil {
		var cmd string
		if json.Unmarshal([]byte(m[1]), &cmd) == nil && cmd != "" {
			return "Bash", map[string]any{"command": cmd}, true
		}
	}
	return "", nil, false
}

func toolUse(id, name string, args map[string]any) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": args}
}

func textBlock(text string) map[string]any { return map[string]any{"type": "text", "text": text} }

func message(role, text string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"role": role, "content": text})
	return b
}

func blocks(role string, content ...map[string]any) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"role": role, "content": content})
	return b
}

// inputText is the text of a content list ([{type: input_text|output_text, text}]),
// a bare string, or a tool output.
func inputText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var out []string
	for _, p := range parts {
		out = append(out, p.Text)
	}
	return strings.Join(out, "")
}

func ordinalID(l line) string { return fmt.Sprintf("ordinal-%d", l.Ordinal) }

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// SubagentFiles implements harness.SubagentLocator. A Codex sub-agent is a rollout of
// its own beside every other (sessions/YYYY/MM/DD/), whose first line, session_meta,
// names the thread that spawned it (source.subagent.thread_spawn.parent_thread_id;
// recorded: harness-mocks codex-mock subagent-transcripts-v2). So the sub-agents of a
// rollout are found by reading the first line of the others, and a sub-agent's own
// children the same way.
func (Transcripts) SubagentFiles(transcriptPath string) []harness.SubagentFile {
	sessions := filepath.Dir(transcriptPath)
	for i := 0; i < 3; i++ { // sessions/YYYY/MM/DD/<rollout>
		sessions = filepath.Dir(sessions)
	}
	all, _ := filepath.Glob(filepath.Join(sessions, "*", "*", "*", "rollout-*.jsonl"))
	parentOf := map[string]string{} // rollout path -> parent thread id
	idOf := map[string]string{}
	for _, p := range all {
		idOf[p] = rolloutThreadID(p)
		parentOf[p] = rolloutParent(p)
	}
	want := map[string]bool{rolloutThreadID(transcriptPath): true}
	var out []harness.SubagentFile
	for grew := true; grew; {
		grew = false
		for _, p := range all {
			if p == transcriptPath || !want[parentOf[p]] || want[idOf[p]] {
				continue
			}
			want[idOf[p]] = true
			grew = true
			out = append(out, harness.SubagentFile{Path: p, Rel: filepath.Base(p)})
		}
	}
	return out
}

// ParentRecord implements harness.ParentLocator: the rollout of the thread the sub-agent's
// session_meta names as its parent (recorded: harness-mocks codex-mock nested-subagents),
// found beside it in the sessions tree. "" for a root, or when that rollout is not there.
func (Transcripts) ParentRecord(transcriptPath string) string {
	parent := rolloutParent(transcriptPath)
	if parent == "" {
		return ""
	}
	sessions := filepath.Dir(transcriptPath)
	for i := 0; i < 3; i++ { // sessions/YYYY/MM/DD/<rollout>
		sessions = filepath.Dir(sessions)
	}
	return FindRollout(filepath.Dir(sessions), parent)
}

// rolloutThreadID is the thread id a rollout's file name ends with.
func rolloutThreadID(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if len(base) < 36 {
		return ""
	}
	return base[len(base)-36:]
}

// rolloutParent is the parent thread a rollout's session_meta names, "" for a root.
func rolloutParent(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var l struct {
		Payload struct {
			ParentThreadID string `json:"parent_thread_id"`
		} `json:"payload"`
	}
	if json.NewDecoder(f).Decode(&l) != nil {
		return ""
	}
	return l.Payload.ParentThreadID
}

// ConfigDir implements harness.Transcripts: $CODEX_HOME, else ~/.codex.
func (Transcripts) ConfigDir() string { return ConfigDir() }

// EncodeProjectDir implements harness.Transcripts. Codex does not group sessions
// by project, so there is no encoding; the directory itself is returned.
func (Transcripts) EncodeProjectDir(dir string) string { return dir }

// ProjectDir implements harness.Transcripts. Codex keeps every session under one
// date-sharded tree, so there is no per-project directory to name; the sessions
// root stands in, and a session is found by its id (FindRollout).
func (Transcripts) ProjectDir(configDir, dir string) string {
	if configDir == "" {
		return ""
	}
	return filepath.Join(configDir, "sessions")
}

// ConfigDir is Codex's home: $CODEX_HOME when set, else ~/.codex. Empty when neither
// can be resolved.
func ConfigDir() string {
	if v := strings.TrimSpace(os.Getenv("CODEX_HOME")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

// FindRollout is the rollout of the session with this id: the file
// sessions/YYYY/MM/DD/rollout-<timestamp>-<id>.jsonl. Empty when there is none.
func FindRollout(configDir, sessionID string) string {
	if configDir == "" || sessionID == "" || strings.ContainsAny(sessionID, `/\*?[`) {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(configDir, "sessions", "*", "*", "*", "rollout-*-"+sessionID+".jsonl"))
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1]
}

// ListRecords implements harness.RecordLister: every rollout of the sessions tree.
func (Transcripts) ListRecords(projectDir string) []string {
	var out []string
	_ = filepath.WalkDir(projectDir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasPrefix(d.Name(), "rollout-") && strings.HasSuffix(d.Name(), ".jsonl") {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}
