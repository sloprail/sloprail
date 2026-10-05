package transcript

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A call answered with an error, or a receipt in other words, names no agent: its result still
// answers it, so the cursor does not carry it for ever.
func TestTheCursorDropsACallAnsweredWithoutAReceipt(t *testing.T) {
	p := newProject(t)
	path := p.write("r",
		namedCall("a1", "", "toolu_e", "Agent", bgInput),
		`{"type":"user","uuid":"r1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_e","is_error":true,"content":"it failed"}]}}`,
		namedCall("a2", "", "toolu_w", "Agent", bgInput),
	)
	got, cur, err := BackgroundAgentSignalsSince(path, AgentSignalCursor{})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Equal(t, []string{"toolu_w"}, cur.Background, "the answered call is gone, the waiting one stays")
}

// A rewritten record is read from its start: in place at the same length, with a longer head, or
// truncated and grown again with other content; a cursor with an offset and no fingerprint too.
func TestTheCursorStartsOverWhenTheRecordWasRewritten(t *testing.T) {
	p := newProject(t)
	first := append(launch("1", "toolu_1", "bg1"), launch("2", "toolu_2", "bg2")...)
	path := p.write("r", first...)
	_, cur, err := BackgroundAgentSignalsSince(path, AgentSignalCursor{})
	require.NoError(t, err)
	size := cur.Offset

	// The same length, other bytes at the end of what was read.
	rewritten := strings.Replace(strings.Join(first, "\n")+"\n", "bg2", "bgX", 1)
	require.Equal(t, int(size), len(rewritten))
	require.NoError(t, os.WriteFile(path, []byte(rewritten+notification("bgX", "completed")+"\n"), 0o644))
	got, _, err := BackgroundAgentSignalsSince(path, cur)
	require.NoError(t, err)
	assert.Len(t, got, 3, "a same-length rewrite is read whole: two launches and the notification")

	// Truncated and grown again with other content, longer than the cursor.
	other := append(launch("7", "toolu_7", "bg7"), launch("8", "toolu_8", "bg8")...)
	other = append(other, launch("9", "toolu_9", "bg9")...)
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(other, "\n")+"\n"), 0o644))
	got, _, err = BackgroundAgentSignalsSince(path, cur)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "bg7", got[0].AgentID)

	// An offset with no fingerprint (a damaged meta) starts over as well.
	got, _, err = BackgroundAgentSignalsSince(path, AgentSignalCursor{Path: path, Offset: size})
	require.NoError(t, err)
	assert.Len(t, got, 3)
}

// A notification that precedes its launch, with the two read apart, is what a whole read gives.
func TestANotificationBeforeItsLaunchIsTheSameInPieces(t *testing.T) {
	p := newProject(t)
	lines := append([]string{notification("bg1", "completed")}, launch("1", "toolu_1", "bg1")...)
	whole, err := BackgroundAgentSignals(p.write("whole", lines...))
	require.NoError(t, err)
	path := p.write("piece", lines[0])
	first, cur, err := BackgroundAgentSignalsSince(path, AgentSignalCursor{})
	require.NoError(t, err)
	appendLines(t, path, lines[1:]...)
	rest, _, err := BackgroundAgentSignalsSince(path, cur)
	require.NoError(t, err)
	assert.Equal(t, whole, append(first, rest...))
}
