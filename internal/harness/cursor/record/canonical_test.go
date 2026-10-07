package record

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type toolUse struct {
	Name  string
	Input map[string]any
}

func toolUses(t *testing.T, line []byte) []toolUse {
	t.Helper()
	r, err := Transcripts{}.ParseRecord(line)
	require.NoError(t, err)
	if len(r.Message) == 0 {
		return nil // the turn_ended marker
	}
	var m struct {
		Content []struct {
			Type  string         `json:"type"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(r.Message, &m))
	var out []toolUse
	for _, c := range m.Content {
		if c.Type == "tool_use" {
			out = append(out, toolUse{c.Name, c.Input})
		}
	}
	return out
}

// The recorded file-tools transcript (harness-mocks cursor-mock runs/file-tools): Write,
// Read and StrReplace come out as the canonical Write, Read, Edit with file_path/content.
func TestRecordedTranscriptToolsAreCanonical(t *testing.T) {
	f, err := os.Open("../testdata/file-tools.transcript.jsonl")
	require.NoError(t, err)
	defer f.Close()
	var got []toolUse
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for sc.Scan() {
		if _, err := (Transcripts{}).ParseRecord(sc.Bytes()); err != nil {
			continue
		}
		got = append(got, toolUses(t, sc.Bytes())...)
	}
	require.Len(t, got, 3)
	assert.Equal(t, toolUse{"Write", map[string]any{"file_path": "<RUN>/note.txt", "content": "hi\n"}}, got[0])
	assert.Equal(t, toolUse{"Read", map[string]any{"file_path": "<RUN>/note.txt"}}, got[1])
	assert.Equal(t, toolUse{"Edit", map[string]any{"file_path": "<RUN>/note.txt", "old_string": "hi", "new_string": "bye"}}, got[2])
}

func TestShellIsBashAndOtherToolsKeepTheirName(t *testing.T) {
	got := toolUses(t, []byte(`{"role":"assistant","message":{"content":[{"type":"text","text":"x"},{"type":"tool_use","name":"Shell","input":{"command":"ls"}},{"type":"tool_use","name":"Grep","input":{"pattern":"a"}}]}}`))
	require.Len(t, got, 2)
	assert.Equal(t, toolUse{"Bash", map[string]any{"command": "ls"}}, got[0])
	assert.Equal(t, "Grep", got[1].Name)
}

func TestAMessageWithoutToolsOnlyGainsItsRole(t *testing.T) {
	line := `{"role":"user","message":{"content":[{"type":"text","text":"hi"}]}}`
	r, err := Transcripts{}.ParseRecord([]byte(line))
	require.NoError(t, err)
	assert.JSONEq(t, `{"role":"user","content":[{"type":"text","text":"hi"}]}`, string(r.Message))
}

func TestWithRoleOnANullMessage(t *testing.T) {
	r, err := Transcripts{}.ParseRecord([]byte(`{"role":"user","message":null}`))
	require.NoError(t, err)
	assert.Equal(t, "null", string(r.Message))
}

func TestWithRole(t *testing.T) {
	got := string(withRole([]byte(`{"content":[{"type":"text","text":"hi"}]}`), "user"))
	assert.Contains(t, got, `"role":"user"`)
	assert.Equal(t, `{"role":"assistant"}`, string(withRole([]byte(`{"role":"assistant"}`), "user")))
}
