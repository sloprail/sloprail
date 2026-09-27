package transcript

import "encoding/json"

// The events `normalize` re-derives from an entry come from two places in it:
// the tool calls an assistant turn made (each a command or a file write the
// command/file modules parse), and the prose the agent wrote (the tags the tag
// module scans). Both are read out of `message.content`, and both are the
// harness's shape rather than this package's — so the reading lives here, beside
// the other readers of that field (ToolUseIDs, the query env), and hands the
// caller plain values it can drive the modules with without re-teaching every
// caller what a Claude Code message looks like.
//
// This is the same split the whole engine draws: which entries are the agent's
// and which blocks are tool calls is a transcript question answered here; what a
// command LINE runs, or which `#tag` tokens sit in TEXT, is the module's own.

// ToolCall is one tool_use block on an assistant entry: what the harness called
// the tool, and the arguments it was given, left undecoded.
//
// The arguments are kept raw for the reason the modules keep them raw — what
// they mean is the tool's business, and each module reads only the keys it
// recognises (a shell tool's `command`, a write tool's `file_path`). Decoding
// them here would be this package modelling every tool's input, which is exactly
// what normalising into Entry avoids.
type ToolCall struct {
	// Name is what the harness calls the tool — `Bash`, `Write`, `Edit`. Read
	// only to decide nothing here: the modules dispatch on the argument SHAPE,
	// not the name (see filemod.extractPending), so this is carried for a caller
	// that builds a module payload, which needs a Tool() to hand back.
	Name string

	// Input is the tool's own arguments, exactly as the harness wrote them.
	Input json.RawMessage
}

// assistantContentBlock is one block of an assistant entry's content list, in
// the fields the two readers here need: the discriminator, a tool call's name
// and input, and a text block's prose. A block that is not the shape being read
// contributes nothing rather than being an error — an assistant turn mixes text
// and tool_use blocks freely.
type assistantContentBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	Text  string          `json:"text"`
}

// assistantContent is the envelope an assistant entry's content sits in. On an
// assistant turn content is a list of blocks; on a user turn the same field is
// frequently a bare string, so it is decoded as raw and dispatched on its JSON
// shape — a bare string simply yields no blocks, which is the right answer for
// "this entry made no tool call and wrote no assistant prose".
type assistantContent struct {
	Content json.RawMessage `json:"content"`
}

// ToolCalls returns every tool_use block in an assistant entry's message, in the
// order they appear.
//
// Empty for anything that is not an assistant turn carrying tool calls — a user
// message whose content is a string, an entry with no message, an assistant turn
// of plain text. That breadth is deliberate and matches ToolUseIDs: `normalize`
// walks every entry and asks each what it yields, and an entry that made no tool
// call contributes no events rather than being a case to skip around.
//
// Only assistant entries are read. A tool_result on a USER entry carries a
// `content` that is not a tool CALL — the tool already ran — so reading a user
// entry here would invent calls out of results. The events `normalize` derives
// are the pre-action ones (what a call was ABOUT to do), which only an assistant
// entry's tool_use blocks describe.
func ToolCalls(e Entry) []ToolCall {
	if e.Type != EntryAssistant || len(e.Message) == 0 {
		return nil
	}
	var msg assistantContent
	if json.Unmarshal(e.Message, &msg) != nil || len(msg.Content) == 0 {
		return nil
	}
	var blocks []assistantContentBlock
	if json.Unmarshal(msg.Content, &blocks) != nil {
		// A bare-string content (the plain-reply shape) does not unmarshal into a
		// list, and it holds no tool call — the right answer is none.
		return nil
	}
	var calls []ToolCall
	for _, b := range blocks {
		if b.Type != "tool_use" || b.Name == "" {
			continue
		}
		calls = append(calls, ToolCall{Name: b.Name, Input: b.Input})
	}
	return calls
}

// AssistantText pulls the prose out of an assistant entry's message — the text a
// tag may sit in — whatever shape the harness recorded it in.
//
// Claude Code writes an assistant `message.content` two ways: as a bare string
// for a plain reply, and as a list of typed blocks (`{"type":"text",...}`
// alongside `{"type":"tool_use",...}`) for a turn that also calls tools. A tag
// lives in the prose, so only text is read, and a tool_use block's arguments are
// deliberately NOT scanned: a `#tag` inside a file the agent wrote is that file's
// business, not a tag the agent declared in its message.
//
// This is the same extraction session_stop.go's assistantText performs to feed
// the tag module at a cycle's end. It lives here so `normalize` gathers an
// entry's text the same way the Stop dispatch does, rather than re-teaching the
// message shape a second time — the shape is the harness's, and one reader of it
// is one thing to keep in step with the format.
//
// Text blocks are joined with newlines, so a `#tag` at the end of one block and a
// word at the start of the next do not fuse into a single false token — the same
// boundary the scanner's own pattern relies on. Empty for a non-assistant entry,
// an entry with no message, or an undecodable one.
func AssistantText(e Entry) string {
	if e.Type != EntryAssistant || len(e.Message) == 0 {
		return ""
	}
	var msg assistantContent
	if json.Unmarshal(e.Message, &msg) != nil || len(msg.Content) == 0 {
		return ""
	}
	// The plain-reply shape: content is a bare string.
	var s string
	if json.Unmarshal(msg.Content, &s) == nil {
		return s
	}
	// The block-list shape: keep the text of the text blocks, joined.
	var blocks []assistantContentBlock
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
