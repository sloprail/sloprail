package gitrepo

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SameAsHead names the substantive commits that left the file as head has it: a later commit
// that restored the state is not among them, the commit that made the state is.
func TestFileHistory_SameAsHeadIsTheCommitThatMadeTheRestoredState(t *testing.T) {
	dir := initRepo(t)
	base := commitFile(t, dir, "a.md", "a\n", "seed")
	c1 := commitFile(t, dir, "a.md", "state\n", "make the state")
	c2 := commitFile(t, dir, "a.md", "state\ntweak\n", "tweak")
	c3 := commitFile(t, dir, "a.md", "state\n", "restore")

	oid := strings.TrimSpace(git(t, dir, "rev-parse", c3+":a.md"))
	res, err := FileHistory(dir, base, c3, []FileRef{{Path: "a.md"}}, map[string]string{"a.md": oid})
	require.NoError(t, err)
	assert.Equal(t, []string{c1, c2, c3}, res.Commits["a.md"])
	assert.Equal(t, []string{c1, c2, c3}, res.Substantive["a.md"])
	assert.Equal(t, []string{c1, c3}, res.SameAsHead["a.md"], "the commit that made the state, and the one that restored it")

	none, err := FileHistory(dir, base, c3, []FileRef{{Path: "a.md"}}, nil)
	require.NoError(t, err)
	assert.Empty(t, none.SameAsHead["a.md"])
}
