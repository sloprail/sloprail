package repo

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Every Go module in the repo is tidy. The plugin e2e modules under
// marketplace/plugins/*/tests build against the root module, so a change to the
// root go.mod (a new import in a shared package) leaves them needing
// `go mod tidy`; nothing else notices until the plugins shard fails on its
// first build. This runs on every PR instead.
func TestGoModulesAreTidy(t *testing.T) {
	root := repoRoot(t)
	out, err := exec.Command("git", "-C", root, "ls-files", "go.mod", "*/go.mod").Output()
	if err != nil {
		t.Fatalf("listing tracked go.mod files: %v", err)
	}
	mods := strings.Fields(string(out))
	sort.Strings(mods)
	if len(mods) == 0 {
		t.Fatal("no tracked go.mod found")
	}
	for _, mod := range mods {
		rel := filepath.Dir(mod)
		t.Run(rel, func(t *testing.T) {
			cmd := exec.Command("go", "mod", "tidy", "-diff")
			cmd.Dir = filepath.Join(root, rel)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("%s needs `go mod tidy` (run it in %s):\n%s", mod, rel, out)
			}
		})
	}
}
