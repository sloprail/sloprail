package record

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
// sloprail keeps the outputs itself. `sr-session post-tool` appends what Cursor's hooks
// report (preToolUse, postToolUse / postToolUseFailure, beforeReadFile) to a file of
// sloprail's own, keyed by the conversation id; OpenRecord (opener.go) merges the
// outcomes back into the stream the engine reads as tool_result records.
//
// The file is append-only and keyed by conversation_id, which stays the same across
// /compress and a resumed session (harness-mocks runs/compaction-transcript-continuity,
// session-resume), so a compaction or a resume loses nothing. It is never deleted when a
// session ends (a resumed conversation's earlier outputs stay citable); a file is swept
// only when its last write and its transcript are both older than the TTL, or the
// transcript is gone.
//
// WHAT PAIRS A RESULT WITH A CALL. The transcript's tool_use blocks carry no id, and
// Cursor's hooks carry a tool_use_id the transcript does not (measured: no id, no
// generation, no index reaches the transcript). So the pairing is by the START ORDER of
// the calls, which both sides see: the preToolUse hooks fire in the order the calls
// start, and the transcript lists the calls in that order. Each stored `pre` line is a
// call's slot (its tool_use_id, and an identity of the call, Identity); the transcript's
// k-th call with an identity is the k-th slot with it; the outcome is the `post` line
// with that slot's tool_use_id, however late or out of order it completed. When the
// slots and the transcript's calls cannot be lined up exactly (a hook was lost, a call
// has no slot or a slot no call) NOTHING is paired for that identity: a result is never
// guessed onto a call.
//
// What the hooks do NOT carry, recorded (cursor-agent 2026.09.28 / 2026.10.01):
//   - Read's postToolUse output is {"file_path","content_length"}: the bytes come from
//     the beforeReadFile that fires between the Read's pre and post (see Content below).
//   - No timestamp in a payload: the record's is the moment the hook ran.

// Line kinds.
const (
	// KindPre is a call about to run: the slot an outcome is later paired with.
	KindPre = "pre"
	// KindPost is a call's outcome (postToolUse, or postToolUseFailure with IsError).
	KindPost = "post"
	// KindRoot marks the conversation as the session's own root: its sessionStart fired.
	// Cursor fires sessionStart for the session only, never for a sub-agent's conversation,
	// whose tool hooks nonetheless fire under its own conversation_id (recorded in
	// harness-mocks runs/subagent-lifecycle-hooks, nested-subagents, foreground-subagent-result,
	// subagent-transcripts). No payload and no transcript line names a conversation's parent,
	// so this is the only recorded way to tell the root from a sub-agent.
	KindRoot = "root"
	// KindContent is the bytes of a file a Read is about to return (beforeReadFile).
	KindContent = "content"
	// KindFollowup is a followup_message sloprail's stop hook emitted (Output): Cursor
	// writes it into the transcript as a user record, which is then harness-injected,
	// not the person's words.
	KindFollowup = "followup"
)

// StoredLine is one line of the file.
type StoredLine struct {
	Kind string `json:"kind"`

	// ToolUseID is Cursor's, on pre and post lines: what ties an outcome to its slot.
	ToolUseID string `json:"tool_use_id,omitempty"`

	// Tool is the canonical tool name (pre, post).
	Tool string `json:"tool,omitempty"`

	// Key is the call's Identity (pre).
	Key string `json:"key,omitempty"`

	// Path is the file of a Read (pre, content).
	Path string `json:"path,omitempty"`

	// Generation is the agent turn the hook belonged to, when the payload names it.
	Generation string `json:"generation,omitempty"`

	// Output is the tool's output text or a failure's message (post), or the file's bytes
	// (content).
	Output  string `json:"output,omitempty"`
	IsError bool   `json:"is_error,omitempty"`

	// At is when the hook ran, RFC 3339.
	At string `json:"at"`
}

const (
	// MaxOutputBytes caps one stored output. A tool_result is one line of the stream and a
	// line is bounded (internal/transcript.maxRecordBytes, 16 MiB); an output was seen at
	// 54 KB. A longer one is cut and says so.
	MaxOutputBytes = 1 << 20

	// MaxStoreBytes caps one conversation's file. Past it new lines are refused
	// (ErrStoreFull) rather than older ones evicted: the file is append-only so that
	// what a citation names stays where it was.
	MaxStoreBytes = 64 << 20

	// TTL is how long an untouched conversation's file is kept before the sweep removes it.
	TTL = 30 * 24 * time.Hour

	// pendingWindow is how long a Read stays pending for a beforeReadFile if its post never
	// comes (the hook fires within the same call, so this is generous).
	pendingWindow = 10 * time.Minute

	// ResultLineBase is where the line numbers of merged tool_result lines start: result
	// number N of the conversation is cited as line ResultLineBase+N, N being its line in
	// the store. No transcript has that many lines, and the number depends on the store
	// alone, so neither a transcript line nor a later result renumbers it.
	ResultLineBase = 1 << 30
)

var (
	// ErrNoConversation: nothing names the conversation, so there is no file to keep it in.
	ErrNoConversation = errors.New("cursor tool results: no conversation id")
	// ErrStoreFull: the conversation's file is at MaxStoreBytes.
	ErrStoreFull = errors.New("cursor tool results: the conversation's store is full")
)

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

func storeDir() (string, error) {
	root, err := dataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "sloprail", "cursor-tool-results"), nil
}

// ToolResultsPath is the file a conversation's tool results are kept in.
func ToolResultsPath(conversationID string) (string, error) {
	if conversationID == "" || strings.ContainsAny(conversationID, `/\`) || conversationID == "." || conversationID == ".." {
		return "", ErrNoConversation
	}
	dir, err := storeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, conversationID+".jsonl"), nil
}

// Identity is what a call is recognised by, spelt identically from the hook's input and
// from the transcript's: the command of a shell call, the file of a Read, Delete or any
// file-changing tool (an edit is a StrReplace in the transcript and a Write carrying the
// whole new content in the hook, so the file is all they share), and for any other tool
// the whole input, so it pairs only when the two inputs are exactly equal. It is hashed:
// a command can be long.
func Identity(tool string, input json.RawMessage) string {
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	str := func(k string) string { s, _ := in[k].(string); return s }
	var basis string
	switch toolClass(tool) {
	case "Bash":
		basis = "Bash\x00" + str("command")
	case "Read", "Delete", "Write":
		path := str("file_path")
		if path == "" {
			path = str("path")
		}
		basis = toolClass(tool) + "\x00" + path
	default:
		canon, _ := json.Marshal(in) // sorted keys
		basis = tool + "\x00" + string(canon)
	}
	sum := sha256.Sum256([]byte(basis))
	return hex.EncodeToString(sum[:16])
}

// toolClass folds the tools that change a file into one.
func toolClass(name string) string {
	switch name {
	case "Write", "Edit", "MultiEdit", "StrReplace":
		return "Write"
	}
	return name
}

// AppendLine adds one line to the conversation's file. The directory is private (0700)
// and the file too (0600): it holds tool outputs. A full store refuses the line.
func AppendLine(conversationID string, l StoredLine) error {
	path, err := ToolResultsPath(conversationID)
	if err != nil {
		return err
	}
	if len(l.Output) > MaxOutputBytes {
		cut := len(l.Output)
		l.Output = strings.ToValidUTF8(l.Output[:MaxOutputBytes], "") +
			fmt.Sprintf("\n[sloprail: output cut at %d of %d bytes]", MaxOutputBytes, cut)
	}
	if l.At == "" {
		l.At = time.Now().UTC().Format(time.RFC3339Nano)
	}
	line, err := json.Marshal(l)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	sweep(dir)
	if fi, err := os.Stat(path); err == nil && fi.Size()+int64(len(line))+1 > MaxStoreBytes {
		return ErrStoreFull
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
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

// sweep removes files nobody wrote to for TTL (an orphan: its session never ended
// cleanly), at most once a day, marked by the directory's own modification time.
func sweep(dir string) {
	marker := filepath.Join(dir, ".swept")
	if fi, err := os.Stat(marker); err == nil && time.Since(fi.ModTime()) < 24*time.Hour {
		return
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) <= TTL {
			continue // written within the TTL: live
		}
		if transcriptLive(strings.TrimSuffix(e.Name(), ".jsonl")) {
			continue // a resumed conversation keeps its earlier outputs citable
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
	_ = os.WriteFile(marker, nil, 0o600)
	now := time.Now()
	_ = os.Chtimes(marker, now, now)
}

// slot is one call as the store knows it.
type slot struct {
	id, tool, key, path, gen string
	at                       time.Time
	started                  int // the store line index of its pre

	// where the outcome text is: the line index (1-based, the line's number in the store,
	// which is what the result is cited under), its offset and length in the file.
	post, content          ref
	postErr, postRecorded  bool
	bound, ambiguous, open bool
}

type ref struct {
	idx int
	off int64
	n   int
	at  string
}

// result is where the slot's outcome text is, and whether one exists: never an
// ambiguous slot's, and never a call whose hook did not report one.
func (s *slot) result() (r ref, isErr bool, ok bool) {
	if s.ambiguous {
		return ref{}, false, false
	}
	if s.bound && !(s.postRecorded && s.postErr) {
		return s.content, false, true
	}
	if s.postRecorded {
		return s.post, s.postErr, true
	}
	return ref{}, false, false
}

// store is a conversation's file read once into slots.
type store struct {
	path  string
	byKey map[string][]*slot

	// root: a KindRoot line was seen, so the conversation is the session's own.
	root bool

	// followups: the texts sloprail's stop hook emitted as followup_message.
	followups map[string]bool
}

// loadStore streams the file, keeping per line only where it is (not its text: outputs
// are read again when a result is emitted). A missing file is an empty store.
func loadStore(conversationID string) *store {
	st := &store{byKey: map[string][]*slot{}}
	path, err := ToolResultsPath(conversationID)
	if err != nil {
		return st
	}
	f, err := os.Open(path)
	if err != nil {
		return st
	}
	defer f.Close()
	st.path = path

	byID := map[string]*slot{}
	var all []*slot  // slots in the order their calls started
	var open []*slot // Read slots with no post yet, in start order
	br := bufio.NewReader(f)
	var off int64
	for idx := 1; ; idx++ {
		line, err := br.ReadBytes('\n')
		here := off
		off += int64(len(line))
		if len(bytes.TrimSpace(line)) > 0 {
			var l StoredLine
			if json.Unmarshal(line, &l) == nil {
				at, _ := time.Parse(time.RFC3339Nano, l.At)
				where := ref{idx: idx, off: here, n: len(line), at: l.At}
				open = expire(open, l.Generation, at)
				switch l.Kind {
				case KindPre:
					if l.ToolUseID == "" {
						break // no id to pair an outcome on
					}
					if s := byID[l.ToolUseID]; s != nil {
						// The same tool_use_id again. The same call (a hook registered twice) is
						// nothing new. A DIFFERENT tool under the id is a step inside one call:
						// Cursor's write and edit tools first fire a Read pre/post of their own
						// (recorded, runs/file-tools: a Read that fails "File not found" and one
						// of the file about to be edited, each under the id of the Write that
						// follows). The call is the last one named, the effectful one, and what
						// the earlier step reported is not its outcome.
						if s.tool == l.Tool && s.key == l.Key {
							break
						}
						open = without(open, s)
						*s = slot{id: s.id, tool: l.Tool, key: l.Key, path: l.Path, gen: l.Generation, at: at, started: where.idx}
						if s.tool == "Read" && s.path != "" {
							s.open = true
							open = append(open, s)
						}
						break
					}
					s := &slot{id: l.ToolUseID, tool: l.Tool, key: l.Key, path: l.Path, gen: l.Generation, at: at, started: where.idx}
					byID[s.id] = s
					all = append(all, s)
					if s.tool == "Read" && s.path != "" {
						s.open = true
						open = append(open, s)
					}
				case KindRoot:
					st.root = true
				case KindFollowup:
					if st.followups == nil {
						st.followups = map[string]bool{}
					}
					st.followups[l.Output] = true
				case KindPost:
					s := byID[l.ToolUseID]
					if s == nil || s.postRecorded || toolClass(l.Tool) != toolClass(s.tool) {
						// an outcome with no slot, a repeat, or one a step inside the call
						// reported (a Read under a Write's id): paired with nothing
						break
					}
					s.postRecorded, s.postErr, s.post = true, l.IsError, where
					s.open = false
					open = without(open, s)
				case KindContent:
					// The file's bytes are a Read's result only if exactly ONE Read of that file
					// is pending (its pre seen, its post not yet) and takes only one content:
					// the hook fires between that Read's pre and post (measured, cursor-agent
					// 2026.10.01). With none pending the read was not the Read tool's (an
					// attachment, the edit tool reading the file it edits): dropped. With more
					// than one, or a second content for the same Read, nothing says which bytes
					// are which, so those Reads get no result.
					var cands []*slot
					for _, s := range open {
						if s.path == l.Path && (s.gen == "" || l.Generation == "" || s.gen == l.Generation) {
							cands = append(cands, s)
						}
					}
					switch {
					case len(cands) == 1 && !cands[0].bound:
						cands[0].bound, cands[0].content = true, where
					case len(cands) >= 1:
						for _, s := range cands {
							s.ambiguous = true
						}
					}
				}
			}
		}
		if err != nil {
			break // io.EOF, or a read error: what was read stands
		}
	}
	for _, sl := range all {
		st.byKey[sl.key] = append(st.byKey[sl.key], sl)
	}
	return st
}

// expire drops the Reads that can no longer be waiting for a beforeReadFile: another
// generation has begun, or the window has passed.
func expire(open []*slot, gen string, now time.Time) []*slot {
	kept := open[:0]
	for _, s := range open {
		if gen != "" && s.gen != "" && gen != s.gen {
			s.open = false
			continue
		}
		if !now.IsZero() && !s.at.IsZero() && now.Sub(s.at) > pendingWindow {
			s.open = false
			continue
		}
		kept = append(kept, s)
	}
	return kept
}

func without(open []*slot, s *slot) []*slot {
	for i, o := range open {
		if o == s {
			return append(open[:i], open[i+1:]...)
		}
	}
	return open
}

// pairing is the slots the transcript's calls line up with.
//
// For one identity the k-th call is the k-th slot, but only when they line up exactly:
// as many slots as calls, or more slots whose extra calls have not been written to the
// transcript yet (they have no outcome either: in flight). Fewer slots than calls, or a
// slot with an outcome and no call, means a hook was lost or a call is not what the slot
// was: the identity is ambiguous and none of its calls has a result.
func (st *store) pairing(callCounts map[string]int) map[string][]*slot {
	out := map[string][]*slot{}
	for key, calls := range callCounts {
		slots := st.byKey[key]
		if len(slots) < calls {
			continue
		}
		ok := true
		for _, extra := range slots[calls:] {
			if extra.postRecorded || extra.bound {
				ok = false
			}
		}
		// Calls pair with slots by START order, which is the transcript's order only if
		// the calls did not overlap: each must have finished before the next began.
		// Two running together (parallel identical commands) may have started in either
		// order relative to the transcript, so their outputs could swap: no result.
		for i := 0; ok && i+1 < calls; i++ {
			if !slots[i].postRecorded || slots[i].post.idx > slots[i+1].started {
				ok = false
			}
		}
		if ok {
			out[key] = slots[:calls]
		}
	}
	return out
}

// text reads a stored line's output again.
func (st *store) text(f *os.File, r ref) (string, bool) {
	buf := make([]byte, r.n)
	if _, err := f.ReadAt(buf, r.off); err != nil && !errors.Is(err, io.EOF) {
		return "", false
	}
	var l StoredLine
	if json.Unmarshal(buf, &l) != nil {
		return "", false
	}
	return l.Output, true
}

// transcriptLive reports whether the conversation's transcript still exists and was
// written within the TTL: its store is then kept even if no tool ran for a long time.
func transcriptLive(conversationID string) bool {
	if conversationID == "" || strings.ContainsAny(conversationID, `/\*?[`) {
		return false
	}
	matches, _ := filepath.Glob(filepath.Join(ConfigDir(), "projects", "*", "agent-transcripts", conversationID, conversationID+".jsonl"))
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && time.Since(fi.ModTime()) <= TTL {
			return true
		}
	}
	return false
}
