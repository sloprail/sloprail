package transcript

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
	"sort"
)

// AgentSignalCursor is how far a record's background-agent signals have been read: the record, the
// byte after the last line read, and the Agent calls run in the background whose result has not
// come yet (a later line carries it). A record only grows, so a reader that kept this reads only
// what was appended since — a Stop of a session whose record is hundreds of megabytes does not
// decode it again, twice, every turn.
type AgentSignalCursor struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset"`
	// Check fingerprints the bytes the cursor stands behind (the record's start and what ends at
	// Offset): a record rewritten in place, as a compaction may, is read from its start again.
	Check      string   `json:"check,omitempty"`
	Background []string `json:"background,omitempty"`
}

// BackgroundAgentSignalsSince returns the signals of the lines after the cursor, and the cursor
// past them. A cursor for another record, or past the end of this one (it was replaced), starts
// over. Like BackgroundAgentSignals an unparseable line is an error ("unknown") and the cursor
// stays where it was, so the caller reads the same lines again next time.
//
// One cursor is kept per session, for the record it dispatches from: a Stop that alternates between
// two records starts over each time, which costs what reading it whole always did.
//
// A last line with no newline yet is read when it is a whole record and is an error when it is not,
// the same answer BackgroundAgentSignals gives for a torn record.
func BackgroundAgentSignalsSince(path string, cur AgentSignalCursor) ([]AgentSignal, AgentSignalCursor, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, cur, fmt.Errorf("transcript: open %s: %w", path, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, cur, fmt.Errorf("transcript: stat %s: %w", path, err)
	}
	if cur.Path != path || cur.Offset < 0 || cur.Offset > st.Size() || (cur.Offset > 0 && fingerprint(f, cur.Offset) != cur.Check) {
		cur = AgentSignalCursor{Path: path}
	}
	background := map[string]bool{}
	for _, id := range cur.Background {
		background[id] = true
	}
	if _, err := f.Seek(cur.Offset, io.SeekStart); err != nil {
		return nil, cur, fmt.Errorf("transcript: seek %s: %w", path, err)
	}

	var out []AgentSignal
	offset := cur.Offset
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > maxRecordBytes {
			return nil, cur, fmt.Errorf("transcript: read %s: a record is longer than %d bytes", path, maxRecordBytes)
		}
		if len(line) > 0 {
			if body := bytes.TrimSpace(line); len(body) > 0 {
				if mayCarryAgentSignal(body) || namesAwaitedCall(body, background) {
					rec, jerr := parseLine(body)
					if jerr != nil {
						return nil, cur, fmt.Errorf("transcript: parse %s: %w", path, jerr)
					}
					// Folded as it is read, so a call this very read began is awaited by the lines after it.
					out = append(out, agentSignalsOf([]Entry{rec.Entry()}, background)...)
				} else if !json.Valid(body) {
					return nil, cur, fmt.Errorf("transcript: parse %s: not a JSON record", path)
				}
			}
			offset += int64(len(line))
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, cur, fmt.Errorf("transcript: read %s: %w", path, err)
		}
	}

	next := AgentSignalCursor{Path: path, Offset: offset}
	if offset > 0 {
		next.Check = fingerprint(f, offset)
	}
	for id := range background {
		next.Background = append(next.Background, id)
	}
	sort.Strings(next.Background)
	return out, next, nil
}

// fingerprint is a digest of the first bytes of the record and of the bytes that end at offset.
func fingerprint(f *os.File, offset int64) string {
	h := sha256.New()
	buf := make([]byte, 4096)
	head := io.NewSectionReader(f, 0, min(offset, 256))
	_, _ = io.Copy(h, head)
	from := max(offset-int64(len(buf)), 0)
	_, _ = io.Copy(h, io.NewSectionReader(f, from, offset-from))
	return hex.EncodeToString(h.Sum(nil))
}

// namesAwaitedCall is whether a record names a background call still waiting for its result: that
// result is read even when it carries no launch receipt (an error, a receipt in other words), so the
// call is answered and leaves the cursor, which therefore holds only calls really still waiting.
func namesAwaitedCall(line []byte, awaited map[string]bool) bool {
	for id := range awaited {
		if bytes.Contains(line, []byte(id)) {
			return true
		}
	}
	return false
}

// mayCarryAgentSignal is whether a raw record could be (or hold) a signal: a launch is an Agent
// call with run_in_background and the result naming the agent it started (agentId), an end is a
// <task-notification>. Matched on words that survive any JSON escaping, so a record that carries
// none of them is only checked to be JSON, not decoded.
func mayCarryAgentSignal(line []byte) bool {
	return bytes.Contains(line, []byte("run_in_background")) ||
		bytes.Contains(line, []byte("task-notification")) ||
		bytes.Contains(line, []byte("agentId"))
}
