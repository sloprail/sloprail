package transcript

import (
	"bufio"
	"encoding/json"
	"fmt"
)

// A citation is a reference the agent can hand back to a person, and a reference
// that cannot be resolved is no better than a claim. `<path>:<line>` is the one
// an editor, a grep, a reviewer already understands — so what a rule needs from
// this file is the physical line an entry sits on, the one thing the entries
// alone cannot recover once they have been read into a slice.

// LinedEntry is an entry together with the physical line it was read from.
//
// The line is 1-based and counts EVERY physical line of the file, including the
// preamble and bookkeeping lines Read drops — because the number has to name the
// line a person or a tool would jump to in the file itself, not an index into
// the entries that survived filtering. An entry read from the 40th line of the
// jsonl reports 40, whatever was skipped before it.
type LinedEntry struct {
	Entry

	// Line is the 1-based physical line of this entry in the transcript file.
	Line int
}

// ReadLines reads a transcript into entries, each carrying the physical line it
// came from.
//
// It is Read with the line kept. The two are separate calls rather than one
// because almost every caller wants the entries and not the line — the line is
// what `cite` and `normalize` assemble into a resolvable `<path>:<line>`, and
// carrying it on the common path would put a field on every entry that nothing
// else reads. Same skipping rules as Read: a line with no uuid, or one that will
// not parse, is not an entry — but it is still counted, so the lines that follow
// it keep the numbers the file gives them.
func ReadLines(path string) ([]LinedEntry, error) {
	f, err := openRecord(path)
	if err != nil {
		return nil, fmt.Errorf("transcript: open %s: %w", path, err)
	}
	defer f.Close()

	// A dedicated scan rather than scanRecords, because the physical line has to
	// advance on EVERY line of the file — including a line that will not parse,
	// which scanRecords drops before the visit callback ever sees it. Counting in
	// the callback would then undercount: the line after an unparseable one would
	// report the number of the line before it. So the count lives at the scan
	// level here, incremented per physical line, and only the lines that are
	// entries are kept — carrying the number the file gives them.
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxRecordBytes)
	var entries []LinedEntry
	line := 0
	for sc.Scan() {
		line++
		rec, perr := parseLine(sc.Bytes())
		if perr == nil && rec.Line > 0 {
			// A line of the harness's own addition, cited under a number of its own and
			// not part of the file's physical numbering (harness.Record.Line).
			line--
			if rec.UUID != "" {
				entries = append(entries, LinedEntry{Entry: rec.Entry(), Line: rec.Line})
			}
			continue
		}
		if perr != nil {
			continue // an unparseable line is the format having moved; still counted
		}
		if rec.UUID == "" {
			continue
		}
		entries = append(entries, LinedEntry{Entry: rec.Entry(), Line: line})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("transcript: read %s: %w", path, err)
	}
	return entries, nil
}

// toolUseBlock is one block of an assistant entry's content — enough of it to
// recognise a tool call and read the id that a sub-agent's meta.json points back
// at. The rest of the block (its name, its input) is left undecoded: the only
// question this file asks of it is "which call is this", and that is the id.
type toolUseBlock struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// assistantMessage is the envelope an assistant entry's content sits in. content
// is a list of blocks on an assistant turn; on a user turn the same field is
// often a bare string, which is why this is only ever decoded for the entries
// whose content is a list — a bare string unmarshals into the slice as a failure
// and is simply not a tool call, which is the right answer.
type assistantMessage struct {
	Content []toolUseBlock `json:"content"`
}

// toolUseIDs returns the ids of every tool_use block in an entry's message.
//
// Empty for anything that is not an assistant turn carrying tool calls — a user
// message whose content is a string, an entry with no message, an assistant turn
// of plain text. That breadth is deliberate: the caller is looking for one
// particular id across a whole trajectory, and an entry that holds no tool call
// simply contributes none rather than being an error to skip around.
func toolUseIDs(e Entry) []string {
	if len(e.Message) == 0 {
		return nil
	}
	var msg assistantMessage
	if json.Unmarshal(e.Message, &msg) != nil {
		return nil
	}
	var ids []string
	for _, b := range msg.Content {
		if b.Type == "tool_use" && b.ID != "" {
			ids = append(ids, b.ID)
		}
	}
	return ids
}
