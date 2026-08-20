package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

// assistantText is the one piece of harness-shape knowledge PostTagWrite adds,
// so it is pinned directly rather than only through the dispatch.

func TestAssistantText_PlainStringContent(t *testing.T) {
	// The plain-reply shape Claude Code writes for a turn with no tool call.
	got := assistantText(json.RawMessage(`{"role":"assistant","content":"I wrote #update here"}`))
	assert.Equal(t, "I wrote #update here", got)
}

func TestAssistantText_BlockListContent(t *testing.T) {
	// The block-list shape for a turn that mixes prose and a tool call. Only the
	// text blocks are read; a tool_use block's arguments are not scanned.
	raw := json.RawMessage(`{"role":"assistant","content":[
		{"type":"text","text":"first #a"},
		{"type":"tool_use","id":"w1","name":"Write","input":{"file_path":"f","content":"#notatag"}},
		{"type":"text","text":"second #b"}
	]}`)
	got := assistantText(raw)
	assert.Equal(t, "first #a\nsecond #b", got,
		"text blocks joined with newlines; a tool_use's own input is not prose")
}

func TestAssistantText_JoinsWithNewlineSoTagsDoNotFuse(t *testing.T) {
	// A `#tag` at the end of one block and a word at the start of the next must
	// not merge into one token — the newline is the boundary the scanner needs.
	raw := json.RawMessage(`{"content":[{"type":"text","text":"ends with #done"},{"type":"text","text":"starts a word"}]}`)
	assert.Equal(t, "ends with #done\nstarts a word", assistantText(raw))
}

func TestAssistantText_EmptyOrUndecodable(t *testing.T) {
	for name, raw := range map[string]json.RawMessage{
		"empty":            nil,
		"no content key":   json.RawMessage(`{"role":"assistant"}`),
		"malformed":        json.RawMessage(`{not json`),
		"content is null":  json.RawMessage(`{"content":null}`),
		"content is a num": json.RawMessage(`{"content":42}`),
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, "", assistantText(raw))
		})
	}
}

func TestAssistantText_BlocksWithoutTextAreSkipped(t *testing.T) {
	// A turn that is nothing but a tool call has no prose, so no text.
	raw := json.RawMessage(`{"content":[{"type":"tool_use","id":"w1","name":"Bash","input":{"command":"ls"}}]}`)
	assert.Equal(t, "", assistantText(raw))
}
