package sessionstate

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetaKeysAndDelete(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.db"))
	require.NoError(t, err)
	defer s.Close()
	for _, k := range []string{"p:a", "p:b", "q:c", "p"} {
		require.NoError(t, s.SetMeta(k, "v"))
	}
	keys, err := s.MetaKeys("p:")
	require.NoError(t, err)
	assert.Equal(t, []string{"p:a", "p:b"}, keys)
	require.NoError(t, s.DeleteMeta("p:a"))
	require.NoError(t, s.DeleteMeta("never"))
	keys, err = s.MetaKeys("p:")
	require.NoError(t, err)
	assert.Equal(t, []string{"p:b"}, keys)
}
