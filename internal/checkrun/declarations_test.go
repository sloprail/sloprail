package checkrun

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/module"
)

func TestLoadDeclarationsStrict_UnreadableStoreIsAnErrorAbsentOneIsNot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	reg, err := module.NewRegistryForTest()
	require.NoError(t, err)

	root := t.TempDir()
	_, err = LoadDeclarationsStrict(io.Discard, root, reg)
	assert.NoError(t, err, "no .sloprail at all is not a failure")

	if os.Geteuid() == 0 {
		t.Skip("permissions are not enforced for root")
	}
	dir := filepath.Join(root, ".sloprail", "file-guard")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.Chmod(dir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	_, err = LoadDeclarationsStrict(io.Discard, root, reg)
	assert.Error(t, err, "a store that cannot be read must not pass as zero guards")
}
