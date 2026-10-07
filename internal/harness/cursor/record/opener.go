package record

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// OpenRecord implements harness.RecordOpener: the transcript at path as the engine
// reads it, with what sloprail kept of the tools' outputs merged in.
//
// Two things are done to the stream, each only ever in terms of the lines before it, so
// appending to the transcript or to the stored results never renumbers what was there:
//
//   - Every tool_use block gets an id, `cursor-L<line>-<n>` (the transcript's own
//     physical line and the block's place in it): Cursor writes none, and a tool_use
//     without one cannot be joined to its result.
//   - After an assistant line holding tool_use blocks, one user line follows with a
//     tool_result block for each call a stored result answers, in the blocks' order.
//     A call with no stored result (a tool whose hook did not run, or has not yet)
//     simply has no result line.
//
// A stored result answers the first tool_use not yet answered with the same tool and
// the same primary argument (the command of a shell call, the file of a file tool):
// the transcript names no call id to match on, so the order Cursor ran them in is what
// pairs them. The line numbers are the merged stream's; the engine opens every record
// the same way, so a `<path>:<line>` it prints resolves against the stream it was made
// from.
func (Transcripts) OpenRecord(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	results := loadResults(Transcripts{}.ConversationID(path))
	pr, pw := io.Pipe()
	go func() {
		defer f.Close()
		pw.CloseWithError(mergeResults(f, pw, results))
	}()
	return pr, nil
}

// RecordVersion implements harness.RecordOpener: the transcript's size and
// modification time, combined with the stored results' (both only grow).
func (Transcripts) RecordVersion(path string) (int64, time.Time, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, time.Time{}, err
	}
	size, mod := fi.Size(), fi.ModTime()
	if side, err := ToolResultsPath(Transcripts{}.ConversationID(path)); err == nil {
		if si, err := os.Stat(side); err == nil {
			size += si.Size()
			if si.ModTime().After(mod) {
				mod = si.ModTime()
			}
		}
	}
	return size, mod, nil
}

// mergeResults copies the transcript to w as OpenRecord describes.
func mergeResults(r io.Reader, w io.Writer, results []*pendingResult) error {
	br := bufio.NewReaderSize(r, 64*1024)
	for n := 1; ; n++ {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := bytes.TrimRight(line, "\r\n")
			out, calls := augment(trimmed, n)
			if _, werr := w.Write(append(out, '\n')); werr != nil {
				return werr
			}
			if blocks, at := resultBlocks(calls, results); len(blocks) > 0 {
				synth, _ := json.Marshal(map[string]any{
					"role":      "user",
					"message":   map[string]any{"content": blocks},
					"timestamp": at,
				})
				if _, werr := w.Write(append(synth, '\n')); werr != nil {
					return werr
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// call is one tool_use block of an assistant line.
type call struct {
	id    string
	tool  string
	input map[string]json.RawMessage
}

// augment gives the tool_use blocks of an assistant line their ids and canonical form
// and returns them. Any other line is returned unchanged.
func augment(line []byte, n int) ([]byte, []call) {
	var top map[string]json.RawMessage
	if json.Unmarshal(line, &top) != nil {
		return line, nil
	}
	var role string
	_ = json.Unmarshal(top["role"], &role)
	if role != "assistant" {
		return line, nil
	}
	msg := canonicalMessage(top["message"])
	var m map[string]json.RawMessage
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(msg, &m) != nil || json.Unmarshal(m["content"], &blocks) != nil {
		return line, nil
	}
	var calls []call
	for i, b := range blocks {
		var typ string
		_ = json.Unmarshal(b["type"], &typ)
		if typ != "tool_use" {
			continue
		}
		var id string
		_ = json.Unmarshal(b["id"], &id)
		if id == "" {
			id = fmt.Sprintf("cursor-L%d-%d", n, i)
			b["id"], _ = json.Marshal(id)
		}
		c := call{id: id}
		_ = json.Unmarshal(b["name"], &c.tool)
		_ = json.Unmarshal(b["input"], &c.input)
		calls = append(calls, c)
	}
	if len(calls) == 0 {
		return line, nil
	}
	m["content"], _ = json.Marshal(blocks)
	top["message"], _ = json.Marshal(m)
	out, err := json.Marshal(top)
	if err != nil {
		return line, nil
	}
	return out, calls
}

// resultBlocks pairs each call with the stored result that answers it, and says when
// the first of them was recorded (the line's timestamp).
func resultBlocks(calls []call, results []*pendingResult) (blocks []map[string]any, at string) {
	for _, c := range calls {
		r := take(results, c.tool, c.input)
		if r == nil {
			continue
		}
		b := map[string]any{"type": "tool_result", "tool_use_id": c.id, "content": r.Output}
		if r.IsError {
			b["is_error"] = true
		}
		if at == "" {
			at = r.At
		}
		blocks = append(blocks, b)
	}
	return blocks, at
}
