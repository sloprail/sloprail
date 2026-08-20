package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// cycleAgentMessages is the text the agent itself wrote over this cycle, one
// string per assistant message, for the tag module to scan.
//
// This is the transcript half of PostTagWrite, and it lives here rather than in
// the module for the reason the whole engine is split this way: which entries
// are the AGENT's, and which are THIS CYCLE's, are questions about a harness's
// record that this service already answers for `session query`, against a format
// that is not the module's to keep stable. The module does the one thing that is
// the tag vocabulary's own — find `#tag` tokens in text.
//
// "The agent's own messages" means assistant entries on the main line of work.
// Sidechains are a sub-agent's, not this agent's, and are left out for the same
// reason `session query` leaves them out by default: a rule about what the agent
// did means the main line. A tag a sub-agent wrote is that sub-agent's own
// cycle's PostTagWrite, dispatched when its Stop fires.
//
// "This cycle" is everything after the last completed cycle's read mark, the
// same position `session query` reads from — so a tag already scanned in a
// completed cycle is not scanned again, and the first cycle of a session sees
// the whole record. A mark that cannot be read yields the whole record, which is
// the safe direction: re-scanning a tag costs a repeated event a context can
// ignore, while skipping one loses the tag for good.
//
// Any failure to read the record yields no messages rather than an error. A
// PostTagWrite carrying an empty list is what the module then emits, which is a
// truthful "no tags seen" for a cycle whose text could not be read — the same
// direction every other read in this dispatch errs in, and never a reason to
// refuse the agent's work.
func cycleAgentMessages(cmd *cobra.Command, store sessionstate.Store, p HookPayload) []string {
	path, err := p.record()
	if err != nil || path == "" {
		// No record to read, or a path this must not read (a guessed file
		// belonging to another conversation). Nothing to scan.
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: tags not scanned: %v\n", err)
		}
		return nil
	}

	entries, err := transcript.Read(path)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: tags not scanned: %v\n", err)
		return nil
	}

	// From the last completed cycle's mark, the same position session query
	// reads from. A mark that cannot be read leaves this at "" — the whole record
	// — which is the safe over-read rather than a skip.
	mark, _, err := store.Meta(sessionstate.MetaTranscriptRead)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: reading the whole session for tags: %v\n", err)
		mark = ""
	}
	entries = transcript.Since(entries, mark)

	var messages []string
	for _, e := range entries {
		if e.Type != transcript.EntryAssistant || e.IsSidechain {
			// Not the agent's own words on the main line of work.
			continue
		}
		if text := assistantText(e.Message); text != "" {
			messages = append(messages, text)
		}
	}
	return messages
}

// assistantText pulls the prose out of an assistant message, whatever shape the
// harness recorded it in.
//
// Claude Code writes an assistant `message.content` two ways: as a bare string
// for a plain reply, and as a list of typed blocks — `{"type":"text","text":…}`
// alongside `{"type":"tool_use",…}` — for a turn that also calls tools. A tag
// lives in the prose, so only `text` blocks are read, and a tool_use block's
// arguments are deliberately NOT scanned: a `#tag` inside a file the agent wrote
// is that file's business (and reaches the file events), not a tag the agent
// declared in its message.
//
// Text blocks are joined with newlines, so a `#tag` at the end of one block and
// a word at the start of the next do not fuse into a single false token — the
// same boundary the scanner's own pattern relies on.
//
// An undecodable message yields "", which the caller drops. It is the harness's
// shape, not this engine's mistake, and a message that will not parse carries no
// tag anyone can act on.
func assistantText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// The two shapes Claude Code writes. content is either a string or a list of
	// blocks; role and the rest are ignored.
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &msg) != nil || len(msg.Content) == 0 {
		return ""
	}

	// The plain-reply shape: content is a bare string.
	var s string
	if json.Unmarshal(msg.Content, &s) == nil {
		return s
	}

	// The block-list shape: content is [{type, text, …}, …].
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(msg.Content, &blocks) != nil {
		return ""
	}
	var out string
	for _, b := range blocks {
		if b.Type != "text" || b.Text == "" {
			continue
		}
		if out != "" {
			out += "\n"
		}
		out += b.Text
	}
	return out
}
