package record

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Cursor's transcript holds no tool_result record: only user and assistant text and
// the assistant's tool_use blocks (true of every recorded transcript, and of the TUI
// recordings in harness-mocks). So everything that grounds a claim in a tool's output
// (`cite --source-types tool_result`, a trajectory's tool_use/tool_result join) has
// nothing to read.
//
// sloprail keeps the outputs itself. Cursor's postToolUse / postToolUseFailure hooks
// carry each call's outcome, which `sr-session post-tool` appends (AppendToolResult)
// to a file of sloprail's own, keyed by the conversation id; OpenRecord (opener.go)
// merges them back into the stream the engine reads as tool_result records, right
// after the tool_use they answer.
//
// The file is append-only and keyed by conversation_id, which stays the same across
// /compress and a resumed session (harness-mocks runs/compaction-transcript-continuity,
// session-resume: session_id, conversation_id and transcript_path unchanged, the
// earlier records kept byte-identical), so a compaction loses nothing.
//
// What the hooks do NOT carry, recorded (cursor-agent 2026.09.28 / 2026.10.01):
//   - Read's postToolUse output is {"file_path","content_length"}: the file's bytes are
//     not in it, so a Read's tool_result states what was read, never its content.
//   - A failed call's output is its error message only (a Shell exit 1 is "Command
//     failed with exit code 1", with no stdout/stderr).
//   - No timestamp: the record's is the moment the hook ran.

// StoredResult is one line of the file: a tool call's outcome in the canonical tool
// vocabulary.
type StoredResult struct {
	// ToolUseID is Cursor's, for a reader of the file; matching a
	// result to its tool_use in the transcript (which carries no id) is by Tool and
	// Input.
	ToolUseID string `json:"tool_use_id"`

	// Kind is "" for a tool's outcome and KindContent for the bytes of a file a Read is
	// about to return (beforeReadFile): kept apart because the Read's own outcome carries
	// only the file's length, and joined to it when the record is read (loadResults).
	Kind string `json:"kind,omitempty"`

	// Tool and Input are the canonical tool name and arguments.
	Tool  string          `json:"tool"`
	Input json.RawMessage `json:"input,omitempty"`

	// Output is the tool's output text, or a failure's error message.
	Output  string `json:"output"`
	IsError bool   `json:"is_error,omitempty"`

	// At is when the hook ran, RFC 3339.
	At string `json:"at"`
}

// KindContent marks a StoredResult that is a file's content, not an outcome.
const KindContent = "content"

// MaxOutputBytes caps one stored output. A tool_result is one line of the stream and a
// line is bounded (internal/transcript.maxRecordBytes, 16 MiB); a Cursor Read or Shell
// output was seen at 54 KB. A longer output is cut and says so.
const MaxOutputBytes = 1 << 20

// ErrNoConversation: nothing names the conversation, so there is no file to keep it in.
var ErrNoConversation = errors.New("cursor tool results: no conversation id")

// dataHome is where sloprail keeps its data between runs, the same place
// internal/sessionpath.DataHome names (XDG_DATA_HOME, else the platform's directory);
// this package may not import it.
func dataHome() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support"), nil
	case "windows":
		if dir := os.Getenv("LocalAppData"); dir != "" {
			return dir, nil
		}
		return filepath.Join(home, "AppData", "Local"), nil
	}
	return filepath.Join(home, ".local", "share"), nil
}

// ToolResultsPath is the file a conversation's tool results are kept in.
func ToolResultsPath(conversationID string) (string, error) {
	if conversationID == "" || strings.ContainsAny(conversationID, `/\`) || conversationID == "." || conversationID == ".." {
		return "", ErrNoConversation
	}
	root, err := dataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "sloprail", "cursor-tool-results", conversationID+".jsonl"), nil
}

// AppendToolResult adds one outcome to the conversation's file. The same call twice
// (a hook registered by two sources runs twice, recorded: hooks-all-matching-run-same-hook)
// is written twice and read once (loadResults drops a repeated tool_use_id).
func AppendToolResult(conversationID string, r StoredResult) error {
	path, err := ToolResultsPath(conversationID)
	if err != nil {
		return err
	}
	if len(r.Output) > MaxOutputBytes {
		cut := len(r.Output)
		r.Output = strings.ToValidUTF8(r.Output[:MaxOutputBytes], "") +
			fmt.Sprintf("\n[sloprail: output cut at %d of %d bytes]", MaxOutputBytes, cut)
	}
	if r.At == "" {
		r.At = time.Now().UTC().Format(time.RFC3339Nano)
	}
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	// One write per line: an append of a single buffer is not interleaved with another
	// process's.
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// loadResults reads the conversation's file in order, once per tool_use_id. A missing
// file is no results; a line that does not parse is skipped.
func loadResults(conversationID string) []*pendingResult {
	path, err := ToolResultsPath(conversationID)
	if err != nil {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []*pendingResult
	seen := map[string]bool{}
	// open is, per file, the Read result made from beforeReadFile's bytes that no Read
	// postToolUse has claimed yet. The hook fires just before the Read's own
	// postToolUse (which carries only the length), so that post is the same call and
	// adds nothing; a second beforeReadFile for the file with none between is the same
	// read seen twice (the agent's edit tool reads the file too, recorded: runs/file-tools)
	// and replaces it. A cursor-agent that fires no postToolUse for a Read (that
	// recording) still has the content as the Read's result.
	open := map[string]*pendingResult{}
	br := bufio.NewReader(f)
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var r StoredResult
			if json.Unmarshal(line, &r) == nil && r.Tool != "" {
				var in map[string]json.RawMessage
				_ = json.Unmarshal(r.Input, &in)
				arg := primaryArg(r.Tool, in)
				switch {
				case r.Kind == KindContent:
					if p := open[arg]; p != nil {
						p.Output = r.Output
					} else {
						p := &pendingResult{StoredResult: r, arg: arg}
						out = append(out, p)
						open[arg] = p
					}
				case r.ToolUseID != "" && seen[r.ToolUseID]:
					// the same hook registered twice
				case r.Tool == "Read" && arg != "" && !r.IsError && open[arg] != nil:
					seen[r.ToolUseID] = r.ToolUseID != ""
					open[arg].ToolUseID = r.ToolUseID
					delete(open, arg) // claimed: its content is this Read's result
				default:
					seen[r.ToolUseID] = r.ToolUseID != ""
					out = append(out, &pendingResult{StoredResult: r, arg: arg})
				}
			}
		}
		if err != nil {
			return out // io.EOF, or a read error: what was read stands
		}
	}
}

// pendingResult is a stored result not yet matched to a tool_use.
type pendingResult struct {
	StoredResult
	arg  string
	used bool
}

// toolClass folds the tools Cursor's hooks report as one (an edit reaches the hooks as a
// Write with the whole new content, the transcript as a StrReplace) into one name.
func toolClass(name string) string {
	switch name {
	case "Write", "Edit", "MultiEdit", "StrReplace":
		return "Write"
	}
	return name
}

// primaryArg is the argument that names which call this is: the command of a Bash, the
// file of a file tool, "" for a tool whose arguments are spelt differently in the
// hook and the transcript.
func primaryArg(tool string, in map[string]json.RawMessage) string {
	key := "file_path"
	if tool == "Bash" {
		key = "command"
	}
	var s string
	if json.Unmarshal(in[key], &s) != nil {
		return ""
	}
	return s
}

// take returns the first unused stored result answering a call of this tool and
// arguments: the same tool class, and the same primary argument (or one side naming
// none), in the order the hooks ran.
func take(results []*pendingResult, tool string, in map[string]json.RawMessage) *pendingResult {
	class, arg := toolClass(tool), primaryArg(tool, in)
	for _, r := range results {
		if r.used || toolClass(r.Tool) != class {
			continue
		}
		if r.arg == arg || r.arg == "" || arg == "" {
			r.used = true
			return r
		}
	}
	return nil
}
