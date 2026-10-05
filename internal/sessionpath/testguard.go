package sessionpath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// refuseRealStoreInTest is the guard that keeps a test process off the developer's own session
// stores. Every sloprail data path (session databases, check results, the eval archive) is
// derived from DataHome, so a test that forgot to point XDG_DATA_HOME or HOME at a temp directory
// would otherwise open the REAL store — and a build under test that carries a newer schema
// would migrate it in place, breaking every installed binary that still knows the old one
// (this happened: a not-yet-installed branch build took a real state.db from schema 7 to 8).
//
// In a test binary the resolved root must lie under the OS temp directory (where t.TempDir and
// the e2e harness's fake HOME live); anything else panics, naming the root and the fix. Outside a
// test binary this is a no-op. Subprocesses a test spawns are not test binaries: the e2e harness
// gives each its own HOME, and the harness's own guard covers them.
func refuseRealStoreInTest(root string) error {
	if !testing.Testing() {
		return nil
	}
	if underTemp(root) {
		return nil
	}
	return fmt.Errorf("sloprail: a test process resolved the real data directory %q (HOME=%q, XDG_DATA_HOME=%q); "+
		"a test must use a temp store: t.Setenv(\"XDG_DATA_HOME\", t.TempDir()) or a fake HOME under t.TempDir()",
		root, os.Getenv("HOME"), os.Getenv("XDG_DATA_HOME"))
}

// underTemp reports whether dir is inside the OS temp directory, symlinks resolved on both sides
// (macOS reports /var where the filesystem holds /private/var).
func underTemp(dir string) bool {
	tmp := resolveExisting(os.TempDir())
	d := resolveExisting(dir)
	rel, err := filepath.Rel(tmp, d)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveExisting resolves symlinks in the longest existing prefix of p and re-appends the rest,
// so a directory that does not exist yet (a store about to be created) still compares correctly.
func resolveExisting(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}
