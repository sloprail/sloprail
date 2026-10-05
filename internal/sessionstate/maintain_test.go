package sessionstate

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A store at the current version whose history is past the bound (a build without the bound wrote
// it, or the migration lost a race with the live session's writes) is repaired by the next open:
// the version is not what decides it. The history carries non-ASCII text, which the guarded write
// compares by character.
func TestOpen_AHistoryPastTheBoundIsCompactedWhateverTheVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	require.NoError(t, err)
	var points []json.RawMessage
	for i := 1; i <= 100; i++ {
		points = append(points, uncited(t, "f", "t", i, "nohup "+strings.Repeat("é", 20000)))
	}
	raw, err := json.Marshal(map[string][]json.RawMessage{"a.md": points, "b.md": points, "c.md": points})
	require.NoError(t, err)
	require.Greater(t, len(raw), bloatedCitations)
	require.NoError(t, s.SetMeta(MetaCitations, string(raw)))
	require.NoError(t, s.Close())

	s, err = Open(path)
	require.NoError(t, err)
	defer s.Close()
	got, _, err := s.Meta(MetaCitations)
	require.NoError(t, err)
	assert.Less(t, len(got), 10_000)
	var h map[string][]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(got), &h))
	assert.Equal(t, []int{1, 100}, atsOf(t, h["a.md"]))
}

func TestHeadAndTailAreWholeCharacters(t *testing.T) {
	s := strings.Repeat("é", 5000)
	assert.Equal(t, 4096*2, len(headOf(s)))
	assert.Equal(t, 4096*2, len(tailOf(s)))
	assert.Equal(t, "abc", headOf("abc"))
	assert.Equal(t, "abc", tailOf("abc"))
}
