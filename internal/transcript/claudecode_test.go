package transcript

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadNormalisesTheFieldsKept checks the translation itself: what Claude
// Code spells, arriving under the canonical names, with nothing else carried
// through.
func TestReadNormalisesTheFieldsKept(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		`{"type":"user","uuid":"u1","parentUuid":null,"isSidechain":false,`+
			`"timestamp":"2026-08-13T09:00:00.000Z","message":{"role":"user","content":"go"},`+
			`"sessionId":"raw","version":"9.9.9","gitBranch":"main","requestId":"req_1"}`,
		`{"type":"assistant","uuid":"u2","parentUuid":"u1","isSidechain":true,`+
			`"timestamp":"2026-08-13T09:00:01.000Z","message":{"role":"assistant","content":"done"},`+
			`"toolUseResult":{"stdout":"ok"}}`,
	)

	entries, err := Read(path)
	require.NoError(t, err, "Read")
	require.Len(t, entries, 2)

	first := entries[0]
	assert.Equal(t, EntryUser, first.Type)
	assert.Equal(t, "u1", first.UUID)
	assert.Empty(t, first.ParentUUID, "the origin record has no parent")
	assert.Equal(t, "2026-08-13T09:00:00.000Z", first.Timestamp)
	assert.False(t, first.IsSidechain)
	assert.Contains(t, string(first.Message), `"content":"go"`, "the message must be carried through")

	second := entries[1]
	assert.Equal(t, EntryAssistant, second.Type)
	assert.Equal(t, "u1", second.ParentUUID)
	assert.True(t, second.IsSidechain)
	assert.Contains(t, string(second.ToolUseResult), `"stdout":"ok"`, "what the tool returned must be carried through")
}

// TestEntryCarriesNothingBeyondTheShape is the field-discipline check: every
// field kept is one each new harness must be normalised into, so an entry
// serialises to those and nothing else. Round-tripping is how a field added
// without thinking gets noticed.
func TestEntryCarriesNothingBeyondTheShape(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session", root("u1"))
	entries, err := Read(path)
	require.NoError(t, err, "Read")

	blob, err := json.Marshal(entries[0])
	require.NoError(t, err, "marshal")
	var got map[string]any
	require.NoError(t, json.Unmarshal(blob, &got))

	allowed := map[string]bool{
		"type": true, "uuid": true, "parentUuid": true, "logicalParentUuid": true,
		"timestamp": true, "isSidechain": true, "message": true, "toolUseResult": true,
	}
	for k := range got {
		assert.True(t, allowed[k],
			"an entry carries %q, which no rule asks about — every field kept is one each new harness must be normalised into", k)
	}
}

// TestReadSkipsRecordsWithoutAUUID: Claude Code writes preamble and bookkeeping
// lines — custom-title, ai-title, mode, queue-operation, last-prompt — that
// describe the session rather than anything that happened in it, and carry no
// uuid at all. They cannot take part in a chain and hold nothing to ask about.
func TestReadSkipsRecordsWithoutAUUID(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		`{"type":"custom-title","customTitle":"x","sessionId":"s"}`,
		`{"type":"ai-title","aiTitle":"y","sessionId":"s"}`,
		`{"type":"mode","mode":"default","sessionId":"s"}`,
		`{"type":"queue-operation","sessionId":"s"}`,
		root("u1"),
	)

	entries, err := Read(path)
	require.NoError(t, err, "Read")
	require.Len(t, entries, 1, "only the record carrying a uuid is an entry")
	assert.Equal(t, "u1", entries[0].UUID)
}

// TestReadKeepsAKindItDoesNotKnow: a harness writes kinds beyond the three a
// rule is usually written against — Claude Code emits `attachment`, among
// others. Renaming those to "something else" would lose which one it was.
func TestReadKeepsAKindItDoesNotKnow(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		`{"type":"attachment","uuid":"u1","parentUuid":null,"isSidechain":false,"timestamp":"t"}`,
	)

	entries, err := Read(path)
	require.NoError(t, err, "Read")
	assert.Equal(t, EntryType("attachment"), entries[0].Type, "an unrecognised kind keeps the harness's own name")
}

// TestReadSkipsALineItCannotParse: the format is read by observation and
// promised by nobody, so one unrecognised line is the harness having changed
// something — a reason to keep reading the rest, not to refuse to answer.
func TestReadSkipsALineItCannotParse(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		root("u1"),
		"{ this is not json",
		record("u2", "u1"),
	)

	entries, err := Read(path)
	require.NoError(t, err, "Read")
	assert.Len(t, entries, 2, "the readable lines either side of a broken one must still be read")
}

// TestReadHandlesARecordPastTheDefaultScanBuffer: one record carries a whole
// tool result, which for a file read or a long command runs far past a
// scanner's default 64KiB. A smaller bound would not fail loudly — it would
// silently stop reading mid-conversation.
func TestReadHandlesARecordPastTheDefaultScanBuffer(t *testing.T) {
	p := newProject(t)
	huge := strings.Repeat("x", 300*1024)
	path := p.write("a-session",
		root("u1"),
		`{"type":"user","uuid":"u2","parentUuid":"u1","isSidechain":false,"timestamp":"t",`+
			`"toolUseResult":{"stdout":"`+huge+`"}}`,
	)

	entries, err := Read(path)
	require.NoError(t, err, "Read")
	assert.Len(t, entries, 2, "a long tool result truncated the read")
}

// TestReadFailsWhenTheFileIsMissing: an absent record is a broken environment,
// not a session that did nothing.
func TestReadFailsWhenTheFileIsMissing(t *testing.T) {
	p := newProject(t)
	_, err := Read(filepath.Join(p.dir, "never-written.jsonl"))
	require.Error(t, err, "reading a missing transcript must be an error")
}
