package cursor

import (
	"bufio"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/harness"
)

// After a compaction Cursor writes the prompt into the transcript again mid-turn
// (recorded: harness-mocks cursor-mock runs/compaction-transcript-continuity), byte-identical
// to the person's. That rewrite is the harness writing: only the person's own record counts.

func compactedFlags(t *testing.T, conv, body string, compacted bool) []bool {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if compacted {
		require.NoError(t, New().(harness.ToolResultRecorder).RecordToolResult(harness.HookInput{SessionID: conv, Event: string(PreCompact)}))
	}
	rc, err := New().Transcripts().(harness.RecordOpener).OpenRecord(placeTranscript(t, conv, body))
	require.NoError(t, err)
	defer rc.Close()
	var out []bool
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for sc.Scan() {
		rec, err := New().Transcripts().ParseRecord(sc.Bytes())
		require.NoError(t, err)
		if rec.Type == string(harness.EntryUser) {
			out = append(out, rec.IsMeta)
		}
	}
	return out
}

const (
	asstLine = `{"role":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}` + "\n"
	endLine  = `{"type":"turn_ended","status":"success"}` + "\n"
)

func TestPromptRewrittenMidTurnAfterACompactionIsHarnessInjected(t *testing.T) {
	p := userLine("<timestamp/>\n<user_query>\nship it\n</user_query>")
	body := p + asstLine + p + userLine("<dynamic_tools>\nx\n</dynamic_tools>") + p + endLine
	assert.Equal(t, []bool{false, true, false, true}, compactedFlags(t, "k1", body, true))
}

func TestADifferentMessageMidTurnAfterACompactionIsTheUsers(t *testing.T) {
	p := userLine("<timestamp/>\n<user_query>\nship it\n</user_query>")
	q := userLine("<timestamp/>\n<user_query>\nactually, stop\n</user_query>")
	assert.Equal(t, []bool{false, true, false}, compactedFlags(t, "k4", p+asstLine+p+q, true))
}

func TestMidTurnUserLineWithoutACompactionIsTheUsers(t *testing.T) {
	// a resumed or forked conversation records the person's prompt after an assistant line
	p := userLine("<timestamp/>\n<user_query>\nship it\n</user_query>")
	assert.Equal(t, []bool{false, false}, compactedFlags(t, "k2", p+asstLine+p, false))
}

func TestPromptOfALaterTurnAfterACompactionIsTheUsers(t *testing.T) {
	p := userLine("<timestamp/>\n<user_query>\nship it\n</user_query>")
	body := p + asstLine + p + endLine + p + asstLine + endLine
	assert.Equal(t, []bool{false, true, false}, compactedFlags(t, "k3", body, true))
}
