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
// Two things are done to the stream:
//
//   - Every tool_use block gets an id, `cursor-L<line>-<n>` (the transcript's own
//     physical line and the block's place in it): Cursor writes none, and a tool_use
//     without one cannot be joined to its result.
//   - After an assistant line's tool_use blocks, one user line follows per call that has
//     a result, holding its tool_result block (see toolresults.go for which calls do:
//     only those whose slot lines up with the transcript exactly).
//
// LINE NUMBERS NEVER SHIFT. A merged line is not part of the transcript's numbering (the
// transcript's own lines keep the numbers the file gives them) and carries a number of its
// own, ResultLineBase plus its line in the store (`sloprail_line`, read back into
// harness.Record.Line). Both are fixed once written: a later result, a later transcript
// line, or a later pairing never renumbers what a citation already names.
func (Transcripts) OpenRecord(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st := loadStore(Transcripts{}.ConversationID(path))

	// First pass: how many calls of each identity the transcript holds, to tell whether
	// the slots line up with them.
	counts := map[string]int{}
	if err := eachCall(f, func(_ int, _ []byte, calls []call) {
		for _, c := range calls {
			counts[c.key]++
		}
	}); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	slots := st.pairing(counts)

	pr, pw := io.Pipe()
	go func() {
		defer f.Close()
		var side *os.File
		if st.path != "" {
			side, _ = os.Open(st.path)
			if side != nil {
				defer side.Close()
			}
		}
		pw.CloseWithError(merge(f, pw, st, side, slots, counts))
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

// merge copies the transcript to w as OpenRecord describes.
func merge(r io.Reader, w io.Writer, st *store, side *os.File, slots map[string][]*slot, counts map[string]int) error {
	seen := map[string]int{}
	return eachLine(r, func(n int, line []byte) error {
		out, calls := augment(line, n)
		if !st.root {
			out = markSidechain(out)
		}
		if _, err := w.Write(append(out, '\n')); err != nil {
			return err
		}
		for _, c := range calls {
			k := seen[c.key]
			seen[c.key]++
			if k >= counts[c.key] { // the transcript grew since it was counted
				continue
			}
			ss := slots[c.key]
			if k >= len(ss) {
				continue
			}
			res, isErr, ok := ss[k].result()
			if !ok || side == nil {
				continue
			}
			text, ok := st.text(side, res)
			if !ok {
				continue
			}
			block := map[string]any{"type": "tool_result", "tool_use_id": c.id, "content": text}
			if isErr {
				block["is_error"] = true
			}
			synth, _ := json.Marshal(map[string]any{
				"role":          "user",
				"message":       map[string]any{"content": []any{block}},
				"timestamp":     res.at,
				"sloprail_line": ResultLineBase + res.idx,
			})
			if _, err := w.Write(append(synth, '\n')); err != nil {
				return err
			}
		}
		return nil
	})
}

// eachLine calls fn for each line of r with its 1-based number, the line's trailing
// newline removed. A final line with no newline is a line.
func eachLine(r io.Reader, fn func(n int, line []byte) error) error {
	br := bufio.NewReaderSize(r, 64*1024)
	for n := 1; ; n++ {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if ferr := fn(n, bytes.TrimRight(line, "\r\n")); ferr != nil {
				return ferr
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

// eachCall calls fn for each assistant line holding tool_use blocks.
func eachCall(r io.Reader, fn func(n int, line []byte, calls []call)) error {
	return eachLine(r, func(n int, line []byte) error {
		if _, calls := augment(line, n); len(calls) > 0 {
			fn(n, line, calls)
		}
		return nil
	})
}

// call is one tool_use block of an assistant line.
type call struct {
	id  string
	key string
}

// augment gives the tool_use blocks of an assistant line their ids and canonical form
// and returns them with their identities. Any other line is returned unchanged.
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
		var id, name string
		_ = json.Unmarshal(b["id"], &id)
		if id == "" {
			id = fmt.Sprintf("cursor-L%d-%d", n, i)
			b["id"], _ = json.Marshal(id)
		}
		_ = json.Unmarshal(b["name"], &name)
		calls = append(calls, call{id: id, key: Identity(name, b["input"])})
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

// markSidechain marks a user line as not the user's words. A conversation that cannot be
// proven to be the session's own root (no sessionStart was seen for it, KindRoot) is a
// sub-agent's, or unknown: its "user" lines are a dispatch prompt, and must never ground a
// citation as the user's. This is the same treatment Claude Code's isSidechain records get.
func markSidechain(line []byte) []byte {
	var top map[string]json.RawMessage
	if json.Unmarshal(line, &top) != nil {
		return line
	}
	var role string
	_ = json.Unmarshal(top["role"], &role)
	if role != "user" {
		return line
	}
	top["sloprail_sidechain"] = json.RawMessage("true")
	out, err := json.Marshal(top)
	if err != nil {
		return line
	}
	return out
}
