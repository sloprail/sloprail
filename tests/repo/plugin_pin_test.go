package repo

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type marketplaceFile struct {
	Plugins []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Source  struct {
			Source string `json:"source"`
			URL    string `json:"url"`
			Path   string `json:"path"`
			Ref    string `json:"ref"`
		} `json:"source"`
	} `json:"plugins"`
}

func readMarketplace(t *testing.T, root string) marketplaceFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".claude-plugin/marketplace.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m marketplaceFile
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Plugins) == 0 {
		t.Fatal("marketplace.json lists no plugins")
	}
	return m
}

// Every plugin is fetched from a git-subdir source pinned to the release tag
// matching its version, never from the marketplace checkout's own HEAD (a
// relative path), so the plugin files and the released binaries share one tag.
func TestMarketplacePinsEveryPluginToItsReleaseTag(t *testing.T) {
	root := repoRoot(t)
	for _, p := range readMarketplace(t, root).Plugins {
		if p.Source.Source != "git-subdir" || p.Source.URL != "sloprail/sloprail" {
			t.Errorf("%s: source is %+v, want git-subdir of sloprail/sloprail", p.Name, p.Source)
		}
		if p.Source.Ref != "v"+p.Version {
			t.Errorf("%s: source.ref is %q, want v%s", p.Name, p.Source.Ref, p.Version)
		}
		if _, err := os.Stat(filepath.Join(root, p.Source.Path, ".claude-plugin/plugin.json")); err != nil {
			t.Errorf("%s: source.path %q is not a plugin directory: %v", p.Name, p.Source.Path, err)
		}
	}
}

// A bump writes the new version AND the new ref, on a copy of the tree so the
// real files are untouched.
func TestBumpVersionWritesVersionAndRef(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatal("jq is required but not installed (brew install jq / apt-get install jq)")
	}
	root := repoRoot(t)
	tmp := t.TempDir()
	copyFile := func(rel string) {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, raw, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	copyFile("scripts/bump-version.sh")
	copyFile("scripts/plugin-manifest-dirs.sh")
	copyFile(".claude-plugin/marketplace.json")
	for _, p := range readMarketplace(t, root).Plugins {
		copyFile(filepath.Join(p.Source.Path, ".claude-plugin/plugin.json"))
	}

	if out, err := exec.Command("bash", filepath.Join(tmp, "scripts/bump-version.sh"), "9.8.7").CombinedOutput(); err != nil {
		t.Fatalf("bump-version.sh: %v\n%s", err, out)
	}

	for _, p := range readMarketplace(t, tmp).Plugins {
		if p.Version != "9.8.7" || p.Source.Ref != "v9.8.7" {
			t.Errorf("%s: version %q ref %q, want 9.8.7 / v9.8.7", p.Name, p.Version, p.Source.Ref)
		}
		if p.Source.Source != "git-subdir" || p.Source.Path == "" {
			t.Errorf("%s: bump damaged the source: %+v", p.Name, p.Source)
		}
	}
}
