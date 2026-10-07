package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCiteNeedle(t *testing.T) {
	cases := []struct {
		name, quote, want string
	}{
		{"longest run wins", "go to the repository now", "repository"},
		{"runs split at JSON-escaped characters", `say "hello" to a\path`, "hello"},
		{"runs split at characters an encoder may escape", "a<b>c&d/efgh", "efgh"},
		{"non-ASCII splits a run", "naïve approach", "approach"},
		{"whitespace splits a run", "foo\tbarbaz\nq", "barbaz"},
		{"too short selects nothing", "to be or", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, string(citeNeedle(c.quote)))
		})
	}
}

// The needle only rules entries out; a quote whose text JSON writes differently
// from how it reads (escapes, unicode, newlines) must still resolve.
func TestCiteFindsQuotesJSONWritesDifferently(t *testing.T) {
	p := newProject(t)
	cur := p.write("cur",
		userMsg("u1", "set \"strict\" mode in C:\\tools\\bin please"),
		userMsg("u2", "the café résumé is <ready> & done/over"),
		userMsg("u3", "first line\nsecond  line\tthird"),
		userMsg("u4", "unrelated words only"))
	for quote, line := range map[string]int{
		`set "strict" mode in C:\tools\bin`:  1,
		`café résumé is <ready> & done/over`: 2,
		"first line second line third":       3,
	} {
		got, err := Cite(cur, quote)
		require.NoError(t, err, quote)
		require.Len(t, got, 1, quote)
		assert.Equal(t, line, got[0].Line, quote)
	}
}

func TestFileMayContainSpansReadBoundaries(t *testing.T) {
	needle := []byte("NEEDLE-ACROSS")
	for _, at := range []int{0, 1<<20 - 5, 1 << 20, 3<<20 + 7} {
		body := []byte(strings.Repeat("x", 4<<20))
		copy(body[at:], needle)
		path := filepath.Join(t.TempDir(), "rec.jsonl")
		require.NoError(t, os.WriteFile(path, body, 0o600))
		assert.True(t, fileMayContain(path, needle), "needle at %d", at)
	}
	path := filepath.Join(t.TempDir(), "none.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x", 3<<20)), 0o600))
	assert.False(t, fileMayContain(path, needle))
	assert.True(t, fileMayContain(filepath.Join(t.TempDir(), "missing"), needle), "an unreadable file might contain it: the real search reports why")
	assert.True(t, fileMayContain(path, nil), "no needle rules nothing out")
}

func TestLoadRecordSeesAppendedEntries(t *testing.T) {
	p := newProject(t)
	cur := p.write("cur", userMsg("u1", "first request here"))
	got, err := Cite(cur, "second request here")
	require.NoError(t, err)
	assert.Empty(t, got)

	f, err := os.OpenFile(cur, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(userMsg("u2", "second request here") + "\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	got, err = Cite(cur, "second request here")
	require.NoError(t, err)
	require.Len(t, got, 1, "a record that grew is parsed again, not served from the cache")
	assert.Equal(t, 2, got[0].Line)
}
