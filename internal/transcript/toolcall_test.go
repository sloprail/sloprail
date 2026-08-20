package transcript

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ToolCalls and AssistantText are the two readings `normalize` drives the modules
// with: the tool_use blocks it hands the command/file modules, and the prose it
// hands the tag module. These pin what each reads out of `message.content`, since
// the shape is the harness's and a wrong read is a silent miss — an entry that
// made a call yielding no event, or a tag that never scanned.

func TestToolCalls_ReadsEveryToolUseBlockInOrder(t *testing.T) {
	e := Entry{
		Type: EntryAssistant,
		Message: rawMessage(`{"role":"assistant","content":[` +
			`{"type":"text","text":"let me run these"},` +
			`{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls -la"}},` +
			`{"type":"tool_use","id":"t2","name":"Write","input":{"file_path":"a.md","content":"hi"}}` +
			`]}`),
	}
	calls := ToolCalls(e)
	assert.Len(t, calls, 2, "both tool_use blocks, and not the text block")
	assert.Equal(t, "Bash", calls[0].Name)
	assert.JSONEq(t, `{"command":"ls -la"}`, string(calls[0].Input), "the tool's arguments, kept raw")
	assert.Equal(t, "Write", calls[1].Name)
	assert.JSONEq(t, `{"file_path":"a.md","content":"hi"}`, string(calls[1].Input))
}

func TestToolCalls_NonAssistantAndEmptyShapesYieldNone(t *testing.T) {
	// A user turn whose content is a bare string is not a tool call.
	user := Entry{Type: EntryUser, Message: rawMessage(`{"role":"user","content":"just text"}`)}
	assert.Nil(t, ToolCalls(user), "a bare-string user message holds no tool call")

	// A user entry carrying a tool_result is a RESULT, not a call — the tool
	// already ran, and normalize derives pre-action events, so this yields none.
	result := Entry{Type: EntryUser, Message: rawMessage(
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"done"}]}`)}
	assert.Nil(t, ToolCalls(result), "a tool_result on a user entry is not a tool call")

	// An assistant turn of plain text makes no call.
	text := Entry{Type: EntryAssistant, Message: rawMessage(
		`{"role":"assistant","content":[{"type":"text","text":"no tools here"}]}`)}
	assert.Nil(t, ToolCalls(text), "an assistant text turn holds no tool call")

	// An assistant plain-reply (bare-string content) makes no call.
	bare := Entry{Type: EntryAssistant, Message: rawMessage(`{"role":"assistant","content":"a plain reply"}`)}
	assert.Nil(t, ToolCalls(bare), "a bare-string assistant reply holds no tool call")

	// No message at all.
	assert.Nil(t, ToolCalls(Entry{Type: EntryAssistant}), "an entry with no message holds no tool call")
}

func TestAssistantText_JoinsTextBlocksAndSkipsToolUse(t *testing.T) {
	e := Entry{
		Type: EntryAssistant,
		Message: rawMessage(`{"role":"assistant","content":[` +
			`{"type":"text","text":"Recording this as #update"},` +
			`{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"echo #notatag"}},` +
			`{"type":"text","text":"and #decision for later"}` +
			`]}`),
	}
	// The two text blocks joined with a newline — and crucially NOT the tool_use
	// block's `command`, so a `#tag` inside a shell argument is never scanned as
	// the agent's declared tag.
	assert.Equal(t, "Recording this as #update\nand #decision for later", AssistantText(e))
}

func TestAssistantText_BareStringReply(t *testing.T) {
	e := Entry{Type: EntryAssistant, Message: rawMessage(`{"role":"assistant","content":"a #plain reply"}`)}
	assert.Equal(t, "a #plain reply", AssistantText(e), "the bare-string reply shape is read whole")
}

func TestAssistantText_NonAssistantYieldsNothing(t *testing.T) {
	user := Entry{Type: EntryUser, Message: rawMessage(`{"role":"user","content":"a user #tag is not the agent's"}`)}
	assert.Empty(t, AssistantText(user), "a user message is not the agent's prose")
	assert.Empty(t, AssistantText(Entry{Type: EntryAssistant}), "an entry with no message has no prose")
}
