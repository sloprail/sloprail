package e2e

import (
	"path/filepath"
	"testing"
)

// real is a path with its symlinks resolved, as the engine registers a folder.
func real(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
