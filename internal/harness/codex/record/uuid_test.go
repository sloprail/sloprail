package record

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every entry of a record has a uuid of its own (Claude Code's do): on a recorded
// rollout no two entries share one, and the call and the output that share a call_id
// are two entries.
func TestParseRecord_UUIDsAreUniquePerEntry(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "testdata", "file-tools.rollout.jsonl"))
	require.NoError(t, err)
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	seen := map[string]int{}
	line := 0
	for sc.Scan() {
		line++
		rec, err := Transcripts{}.ParseRecord(sc.Bytes())
		require.NoError(t, err)
		if rec.UUID == "" {
			continue
		}
		prev, dup := seen[rec.UUID]
		assert.False(t, dup, "line %d repeats the uuid %q of line %d", line, rec.UUID, prev)
		seen[rec.UUID] = line
	}
	assert.Greater(t, len(seen), 10)
}

// An item without an id of its own is named by its line's ordinal, so two such items
// differ; a call and its output (same call_id, no ids) differ too, and a line with no
// ordinal has no uuid rather than a shared one.
func TestParseRecord_IdlessItemsKeepDistinctUUIDs(t *testing.T) {
	parse := func(raw string) string {
		rec, err := Transcripts{}.ParseRecord([]byte(raw))
		require.NoError(t, err)
		return rec.UUID
	}
	user := parse(`{"ordinal":1,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}}`)
	call := parse(`{"ordinal":2,"type":"response_item","payload":{"type":"function_call","call_id":"c1","name":"shell","arguments":"{}"}}`)
	out := parse(`{"ordinal":3,"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"x"}}`)
	assert.Equal(t, "ordinal-1", user)
	assert.NotEqual(t, call, out)
	assert.Empty(t, parse(`{"type":"response_item","payload":{"type":"message","role":"user","content":[]}}`))
}
