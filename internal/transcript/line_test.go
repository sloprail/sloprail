package transcript

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadLinesNumbersEveryPhysicalLine is the property `cite` and `normalize`
// both rest on: the line reported is the physical line of the file, counting the
// preamble and bookkeeping lines Read drops — because the number has to name the
// line a person or a tool jumps to, not an index into the entries that survived.
func TestReadLinesNumbersEveryPhysicalLine(t *testing.T) {
	p := newProject(t)
	// Two preamble lines with no uuid, then the root on physical line 3, an
	// ordinary record on 4, and — after a bookkeeping line on 5 — another on 6.
	path := p.write("a-session",
		preamble(),                         // line 1, no uuid
		`{"type":"mode","mode":"default"}`, // line 2, no uuid
		root("u1"),                         // line 3
		record("u2", "u1"),                 // line 4
		`{"type":"queue-operation","sessionId":"s"}`, // line 5, no uuid
		record("u3", "u2"),                           // line 6
	)

	entries, err := ReadLines(path)
	require.NoError(t, err, "ReadLines")
	require.Len(t, entries, 3, "only uuid-carrying lines are entries")

	assert.Equal(t, "u1", entries[0].UUID)
	assert.Equal(t, 3, entries[0].Line, "the root sits on physical line 3, after two preamble lines")
	assert.Equal(t, "u2", entries[1].UUID)
	assert.Equal(t, 4, entries[1].Line)
	assert.Equal(t, "u3", entries[2].UUID)
	assert.Equal(t, 6, entries[2].Line, "a skipped bookkeeping line still advances the count")
}

// TestReadLinesCountsAnUnparseableLine: a line that will not parse is not an
// entry, but the lines after it must keep the numbers the file gives them — so
// the broken line is counted like any other physical line.
func TestReadLinesCountsAnUnparseableLine(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		root("u1"),          // line 1
		"{ not json at all", // line 2
		record("u2", "u1"),  // line 3
	)

	entries, err := ReadLines(path)
	require.NoError(t, err, "ReadLines")
	require.Len(t, entries, 2)
	assert.Equal(t, 1, entries[0].Line)
	assert.Equal(t, 3, entries[1].Line, "the entry after an unparseable line keeps its physical line number")
}

// TestToolUseIDsReadsAssistantCalls: the ids pulled from an assistant turn's
// content are the tool_use block ids and nothing else — a text block contributes
// none, and a user turn whose content is a bare string contributes none.
func TestToolUseIDsReadsAssistantCalls(t *testing.T) {
	assistant := Entry{
		Type: EntryAssistant,
		Message: rawMessage(`{"role":"assistant","content":[` +
			`{"type":"text","text":"let me run this"},` +
			`{"type":"tool_use","id":"toolu_abc","name":"Bash","input":{"command":"ls"}},` +
			`{"type":"tool_use","id":"toolu_def","name":"Write","input":{}}` +
			`]}`),
	}
	assert.Equal(t, []string{"toolu_abc", "toolu_def"}, toolUseIDs(assistant),
		"only the tool_use blocks' ids, in order")

	// A user turn with a bare string content is not a tool call and yields none —
	// reaching into it as a list must not error.
	user := Entry{Type: EntryUser, Message: rawMessage(`{"role":"user","content":"just text"}`)}
	assert.Nil(t, toolUseIDs(user), "a bare-string user message holds no tool_use id")

	// An entry with no message at all yields none.
	assert.Nil(t, toolUseIDs(Entry{Type: EntryUser}), "an entry with no message holds no tool_use id")
}

// rawMessage is a tiny helper so the fixtures above read as the JSON they are.
func rawMessage(s string) []byte { return []byte(s) }
