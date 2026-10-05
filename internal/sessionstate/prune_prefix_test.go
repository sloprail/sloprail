package sessionstate

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A key whose branch starts with a character that is not ASCII sorts after any prefix+"\x7f" bound:
// the range of a prefix ends at the prefix with its last byte raised.
func TestPruneGone_ABranchWithANonASCIINameIsPrunedLikeAnyOther(t *testing.T) {
	s, err := open(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer s.Close()
	gone := filepath.Join(t.TempDir(), "removed")
	require.NoError(t, s.SetMeta("observed-tip:été:"+gone, "abc"))
	require.NoError(t, s.SetMeta("foreign-cover:日本:"+gone, "x"))
	require.NoError(t, s.SetMeta("refs_snapshot:é", "legacy"))
	live := t.TempDir()
	require.NoError(t, s.SetMeta("observed-tip:été:"+live, "abc"))

	require.NoError(t, pruneGone(s.db, false))

	keys := metaKeys(t, s.db)
	assert.NotContains(t, keys, "observed-tip:été:"+gone)
	assert.NotContains(t, keys, "foreign-cover:日本:"+gone)
	assert.Contains(t, keys, "observed-tip:été:"+live, "a live folder's observation stays")
	assert.Equal(t, "observed-tip;", prefixEnd("observed-tip:"))
}
