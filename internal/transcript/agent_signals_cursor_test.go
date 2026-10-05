package transcript

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	defer f.Close()
	for _, l := range lines {
		_, err := f.WriteString(l + "\n")
		require.NoError(t, err)
	}
}

// Read in pieces, as a Stop per turn reads a growing record, the cursor yields exactly the signals a
// whole read yields, in order — even a launch whose call and result land in different pieces.
func TestSignalsSinceTheCursorAreTheSignalsOfAWholeRead(t *testing.T) {
	p := newProject(t)
	lines := append(append(append(launch("1", "toolu_1", "bg1"), userMsg("u1", "hello")), launch("2", "toolu_2", "bg2")...), notification("bg1", "completed"), notification("bg2", "failed"))
	whole, err := BackgroundAgentSignals(p.write("whole", lines...))
	require.NoError(t, err)
	require.Len(t, whole, 4)

	for split := 1; split < len(lines); split++ {
		path := p.write("piece", lines[:split]...)
		first, cur, err := BackgroundAgentSignalsSince(path, AgentSignalCursor{})
		require.NoError(t, err)
		appendLines(t, path, lines[split:]...)
		rest, cur2, err := BackgroundAgentSignalsSince(path, cur)
		require.NoError(t, err)
		assert.Equal(t, whole, append(first, rest...), "split at %d", split)
		again, _, err := BackgroundAgentSignalsSince(path, cur2)
		require.NoError(t, err)
		assert.Empty(t, again, "nothing was appended, nothing is read: split %d", split)
	}
}

// A torn line after the cursor is an error that leaves the cursor where it was;
// a final record still being written is the same answer, and a whole one with no newline is read.
func TestTheCursorSkipsWhatWasReadAndRefusesATornLine(t *testing.T) {
	p := newProject(t)
	path := p.write("r", launch("1", "toolu_1", "bg1")...)
	_, cur, err := BackgroundAgentSignalsSince(path, AgentSignalCursor{})
	require.NoError(t, err)
	assert.Greater(t, cur.Offset, int64(0))

	appendLines(t, path, "{not json")
	got, again, err := BackgroundAgentSignalsSince(path, cur)
	require.Error(t, err, "a torn record must not be read as a clean answer")
	assert.Nil(t, got)
	assert.Equal(t, cur, again, "the cursor does not move past a line that was not read")

	// A final record still being written, with no newline, is the same answer.
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(launch("1", "toolu_1", "bg1"), "\n")+"\n"+`{"type":"user","uu`), 0o644))
	_, _, err = BackgroundAgentSignalsSince(path, cur)
	require.Error(t, err)
	// ... and a whole record with no newline is read.
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(launch("1", "toolu_1", "bg1"), "\n")+"\n"+notification("bg1", "completed")), 0o644))
	sigs, _, err := BackgroundAgentSignalsSince(path, cur)
	require.NoError(t, err)
	require.Len(t, sigs, 1)
	assert.Equal(t, AgentEnded, sigs[0].Kind)
}

// A cursor for another record, or past the end of a record that was replaced, starts over.
func TestTheCursorStartsOverOnAnotherOrShorterRecord(t *testing.T) {
	p := newProject(t)
	a := p.write("a", append(launch("1", "toolu_1", "bg1"), launch("2", "toolu_2", "bg2")...)...)
	_, cur, err := BackgroundAgentSignalsSince(a, AgentSignalCursor{})
	require.NoError(t, err)

	b := p.write("b", launch("3", "toolu_3", "bg3")...)
	got, _, err := BackgroundAgentSignalsSince(b, cur)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "bg3", got[0].AgentID)

	short := p.write("a", launch("4", "toolu_4", "bg4")...)
	got, _, err = BackgroundAgentSignalsSince(short, cur)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "bg4", got[0].AgentID)
}

// The calls the cursor carries are those still waiting for a result: it does not grow with the record.
func TestTheCursorCarriesOnlyCallsStillWaitingForAResult(t *testing.T) {
	p := newProject(t)
	var lines []string
	for i := 0; i < 200; i++ {
		lines = append(lines, launch(string(rune('a'+i%26))+strings.Repeat("x", i), "toolu_"+strings.Repeat("y", i+1), "bg")...)
	}
	lines = append(lines, namedCall("w", "", "toolu_wait", "Agent", bgInput))
	_, cur, err := BackgroundAgentSignalsSince(p.write("r", lines...), AgentSignalCursor{})
	require.NoError(t, err)
	assert.Equal(t, []string{"toolu_wait"}, cur.Background)
}
