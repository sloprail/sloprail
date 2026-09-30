package declaration

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A guard's identity is its whole `.sloprail` root: the project's own and a
// plugin's have the same layout, `<root>/file-guard/<name>`.
func TestFileGuardRoot_IsTheSloprailDirectoryItWasLoadedFrom(t *testing.T) {
	project := FileGuard{Name: "size", Dir: filepath.Join("/repo", ".sloprail", "file-guard", "size")}
	assert.Equal(t, filepath.Join("/repo", ".sloprail"), project.Root())

	plugin := FileGuard{Name: "size", Dir: filepath.Join("/cache/acme/1.0", ".sloprail", "file-guard", "size")}
	assert.Equal(t, filepath.Join("/cache/acme/1.0", ".sloprail"), plugin.Root())
}
