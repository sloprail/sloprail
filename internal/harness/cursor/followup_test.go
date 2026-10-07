package cursor

import (
	"bufio"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/harness"
)

// A stop hook's followup_message is written into Cursor's transcript as a user record.
// sloprail knows the text it emitted, so exactly that record is harness-injected (IsMeta,
// as Claude Code's Stop-hook feedback), and any other user record stays the person's.

func metaFlags(t *testing.T, conv, body string, followups ...string) []bool {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	h := New().(harness.BlockRecorder)
	for _, f := range followups {
		require.NoError(t, h.RecordBlock(harness.HookInput{SessionID: conv}, f))
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

func userLine(text string) string {
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(text)
	return `{"role":"user","message":{"content":[{"type":"text","text":"` + esc + `"}]}}` + "\n"
}

func TestEmittedFollowupIsHarnessInjectedNotUserAuthored(t *testing.T) {
	const reason = "Refused: you said \"ship it\"; the gate wants a test."
	body := userLine("<timestamp/>\n<user_query>\nship it\n</user_query>") + userLine(reason)
	assert.Equal(t, []bool{false, true}, metaFlags(t, "c1", body, reason))
}

func TestFollowupInCursorsPromptEnvelopeIsStillInjected(t *testing.T) {
	const reason = "Refused: tests missing."
	body := userLine("<timestamp/>\n<user_query>\n" + reason + "\n</user_query>")
	assert.Equal(t, []bool{true}, metaFlags(t, "c2", body, reason))
}

func TestUserTextOnlyResemblingAFollowupIsNotMarked(t *testing.T) {
	const reason = "Refused: tests missing."
	body := userLine(reason+" Please retry.") + userLine("<user_query>\nrefused: tests missing.\n</user_query>")
	assert.Equal(t, []bool{false, false}, metaFlags(t, "c3", body, reason))
}

func TestNoRecordedFollowupMarksNothing(t *testing.T) {
	assert.Equal(t, []bool{false}, metaFlags(t, "c4", userLine("anything")))
}
