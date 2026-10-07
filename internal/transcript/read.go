package transcript

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/sloprail/sloprail/internal/harness"
)

// This file reads a session file into entries. The line format is the
// harness's (harness.Transcripts.ParseRecord, Claude Code's in
// internal/harness/claudecode/record); the walk over lines is the same for all.

// maxRecordBytes bounds one line of a session record. A single record carries a
// whole tool result, which for a file read or a long command is far past the
// scanner's default 64KiB — a smaller bound would not fail loudly, it would
// silently stop reading mid-conversation.
const maxRecordBytes = 16 * 1024 * 1024

// Read reads a Claude Code session record into canonical entries.
//
// Lines carrying no uuid are skipped. Claude Code writes preamble and
// bookkeeping records — `custom-title`, `ai-title`, `mode`, `queue-operation`,
// `last-prompt` — that describe the session rather than anything that happened
// in it, and which carry no uuid at all; they cannot take part in a chain and
// there is nothing in them for a rule to ask about.
//
// A line that will not parse is skipped rather than fatal. The format is read
// by observation and promised by nobody, so one unrecognised line is the
// harness having changed something — a reason to keep reading the rest, not to
// refuse to answer at all. A file that cannot be opened or read IS fatal: see
// the fail-loudly reasoning on StableSessionID.
func Read(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("transcript: open %s: %w", path, err)
	}
	defer f.Close()
	return readFrom(f, path)
}

// parseLine parses one line with the registered harness's format.
func parseLine(line []byte) (harness.Record, error) {
	return harness.Current().Transcripts().ParseRecord(line)
}

func readFrom(r io.Reader, path string) ([]Entry, error) {
	var entries []Entry
	err := scanRecords(r, path, func(rec harness.Record) bool {
		if rec.UUID == "" {
			return true
		}
		entries = append(entries, rec.Entry())
		return true
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// scanRecords walks a record file line by line, handing each parsed record to
// visit. Returning false from visit stops the walk — which is what lets the
// identity walk read a root record without reading a conversation of thousands
// behind it.
func scanRecords(r io.Reader, path string, visit func(harness.Record) bool) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxRecordBytes)
	for sc.Scan() {
		rec, err := parseLine(sc.Bytes())
		if err != nil {
			continue // see Read: an unparseable line is the format having moved
		}
		if !visit(rec) {
			return nil
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("transcript: read %s: %w", path, err)
	}
	return nil
}

// scanFile is scanRecords over a path.
func scanFile(path string, visit func(harness.Record) bool) error {
	f, err := os.Open(path)
	if err != nil {
		// Not re-stating the path: the wrapped error already names it, and a
		// path repeated at every level of a message tells the reader nothing
		// the first mention did not.
		return fmt.Errorf("transcript: %w", err)
	}
	defer f.Close()
	return scanRecords(f, path, visit)
}

// readStrict is Read for a caller that must not act on a partial picture: a line that will
// not parse is an error, not skipped (a skipped line could be the very notification that ends
// an agent's run), and a record without a uuid is kept.
func readStrict(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("transcript: open %s: %w", path, err)
	}
	defer f.Close()
	var entries []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxRecordBytes)
	for sc.Scan() {
		if len(strings.TrimSpace(sc.Text())) == 0 {
			continue
		}
		rec, err := parseLine(sc.Bytes())
		if err != nil {
			return nil, fmt.Errorf("transcript: parse %s: %w", path, err)
		}
		entries = append(entries, rec.Entry())
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("transcript: read %s: %w", path, err)
	}
	return entries, nil
}

// agentLaunchedID matches the id in the receipt a background Agent launch returns:
// "Async agent launched successfully. … agentId: <id> (…)".
var agentLaunchedID = regexp.MustCompile(`agentId:\s*([A-Za-z0-9_-]+)`)

// taskNotificationID and taskNotificationStatus read a <task-notification>'s task id
// (the agent id) and status.
var (
	taskNotificationID     = regexp.MustCompile(`<task-id>\s*([^<\s]+)\s*</task-id>`)
	taskNotificationStatus = regexp.MustCompile(`<status>\s*([a-z_]+)\s*</status>`)
)

// terminalTaskStatuses are the statuses after which a background agent is no longer running.
var terminalTaskStatuses = map[string]bool{"completed": true, "failed": true, "killed": true, "stopped": true}

// AgentSignalKind says what a record of the dispatching transcript shows about a sub-agent.
type AgentSignalKind string

const (
	// AgentLaunched: a background Agent call whose receipt returned the agent's id.
	AgentLaunched AgentSignalKind = "launched"
	// AgentEnded: a <task-notification> for the agent carrying a terminal status.
	AgentEnded AgentSignalKind = "ended"
)

// AgentSignal is one fact the dispatching transcript records about a background sub-agent, in
// record order.
type AgentSignal struct {
	Kind    AgentSignalKind
	AgentID string
	// Status is the notification's terminal status (completed, failed, killed, stopped); "" for a launch.
	Status string
	// Key identifies the record that carried the signal, stable across reads and across files that
	// repeat the record: the entry's uuid, or a hash of the notification when it has none.
	Key string
}

// BackgroundAgentSignals returns what the transcript at path shows about background sub-agents,
// in order: each launch (an Agent tool_use with run_in_background whose result returned an agent
// id) and each <task-notification> with a terminal status. It decides nothing: the registry
// (internal/sessionstate) folds these with the hooks' signals, and RunningBackgroundAgents is
// the fold of this alone. A foreground sub-agent is never listed (its call returns only when it
// is done).
//
// An error means "unknown": the caller learns nothing from this record and an agent it does not
// know otherwise is judged.
func BackgroundAgentSignals(path string) ([]AgentSignal, error) {
	entries, err := readStrict(path)
	if err != nil {
		return nil, err
	}
	return agentSignalsOf(entries, map[string]bool{}), nil
}

// agentSignalsOf is the signals the entries show; background is the tool_use ids of the
// run_in_background Agent calls seen so far, added to and consumed as the entries are read.
func agentSignalsOf(entries []Entry, background map[string]bool) []AgentSignal {
	var out []AgentSignal
	notify := func(e Entry, text string) {
		if id, status, ok := parseTaskNotification(text); ok && terminalTaskStatuses[status] {
			key := e.UUID
			if key == "" {
				sum := sha256.Sum256([]byte(text))
				key = hex.EncodeToString(sum[:8])
			}
			out = append(out, AgentSignal{Kind: AgentEnded, AgentID: id, Status: status, Key: key + ":" + id})
		}
	}
	for _, e := range entries {
		switch e.Type {
		case EntryAssistant:
			for _, c := range ToolCalls(e) {
				if c.Name != "Agent" && c.Name != "Task" {
					continue
				}
				var in struct {
					Background json.RawMessage `json:"run_in_background"`
				}
				// A boolean as Claude Code writes it; the string "true" is what the claude-mock records.
				if json.Unmarshal(c.Input, &in) == nil && (string(in.Background) == "true" || string(in.Background) == `"true"`) {
					background[c.ID] = true
				}
			}
		case EntryUser:
			if len(e.Message) == 0 {
				continue
			}
			var msg assistantContent
			if json.Unmarshal(e.Message, &msg) != nil || len(msg.Content) == 0 {
				continue
			}
			var text string
			if json.Unmarshal(msg.Content, &text) == nil {
				notify(e, text)
				continue
			}
			var blocks []struct {
				Type      string          `json:"type"`
				ToolUseID string          `json:"tool_use_id"`
				IsError   bool            `json:"is_error"`
				Content   json.RawMessage `json:"content"`
				Text      string          `json:"text"`
			}
			if json.Unmarshal(msg.Content, &blocks) != nil {
				continue
			}
			for _, b := range blocks {
				switch {
				case b.Type == "text":
					notify(e, b.Text)
				case b.Type == "tool_result" && background[b.ToolUseID]:
					delete(background, b.ToolUseID) // answered: nothing more to wait for from this call
					if b.IsError {
						continue
					}
					for _, body := range resultBodies(b.Content) {
						if m := agentLaunchedID.FindStringSubmatch(body); m != nil && strings.Contains(body, "launched") {
							out = append(out, AgentSignal{Kind: AgentLaunched, AgentID: m[1], Key: e.UUID + ":" + m[1]})
							break
						}
					}
				}
			}
		case EntryAttachment:
			var a struct {
				Type        string `json:"type"`
				Prompt      string `json:"prompt"`
				CommandMode string `json:"commandMode"`
			}
			if json.Unmarshal(e.Attachment, &a) == nil && a.Type == "queued_command" && a.CommandMode == "task-notification" {
				notify(e, a.Prompt)
			}
		}
	}
	return out
}

// RunningBackgroundAgents returns the ids of the background sub-agents the transcript at path
// shows as still running: launched, with no LATER terminal <task-notification> for that id. A
// fold of BackgroundAgentSignals over this one record; the session's registry is the lasting
// answer, this is what one file says.
//
// An error means "unknown": a caller must read it as "nothing is running" and judge everything.
func RunningBackgroundAgents(path string) (map[string]bool, error) {
	signals, err := BackgroundAgentSignals(path)
	if err != nil {
		return nil, err
	}
	running := map[string]bool{}
	for _, s := range signals {
		if s.Kind == AgentLaunched {
			running[s.AgentID] = true
		} else {
			delete(running, s.AgentID)
		}
	}
	return running, nil
}

// parseTaskNotification reads the agent id and status of a <task-notification>; ok is false for
// text that is not one.
func parseTaskNotification(text string) (id, status string, ok bool) {
	if !strings.HasPrefix(strings.TrimSpace(text), "<task-notification>") {
		return "", "", false
	}
	i, st := taskNotificationID.FindStringSubmatch(text), taskNotificationStatus.FindStringSubmatch(text)
	if i == nil || st == nil {
		return "", "", false
	}
	return i[1], st[1], true
}
