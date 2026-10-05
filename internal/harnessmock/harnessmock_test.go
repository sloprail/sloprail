package harnessmock

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionMatchesPin(t *testing.T) {
	pin, err := os.ReadFile(filepath.Join("..", "..", "tests", "e2e", "harness", "MOCK_VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	if Version == "" || Version != strings.TrimSpace(string(pin)) {
		t.Fatalf("Version %q != tests/e2e/harness/MOCK_VERSION %q: bump both", Version, pin)
	}
}

func TestPathMissingNamesVersionAndInstall(t *testing.T) {
	t.Setenv("A10N_CLAUDE_MOCK", "")
	t.Setenv("PATH", t.TempDir())
	_, err := Path()
	if err == nil || !strings.Contains(err.Error(), Version) || !strings.Contains(err.Error(), "go install") {
		t.Fatalf("err = %v, want it to name %s and how to install", err, Version)
	}
}

func TestPathRejectsNonGoBinary(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "a10n-claude-mock")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("A10N_CLAUDE_MOCK", fake)
	if _, err := Path(); err == nil || !strings.Contains(err.Error(), Version) {
		t.Fatalf("err = %v, want a version complaint naming %s", err, Version)
	}
}

func TestLocalMarketplaceMakesSourcesLocal(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755)
	os.MkdirAll(filepath.Join(root, "marketplace"), 0o755)
	os.WriteFile(filepath.Join(root, ".claude-plugin", "marketplace.json"),
		[]byte(`{"name":"m","plugins":[{"name":"p","source":{"source":"github","ref":"v1"}},{"name":"q","source":"./x"}]}`), 0o644)
	dir := t.TempDir()
	if err := LocalMarketplace(root, dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".claude-plugin", "marketplace.json"))
	if !strings.Contains(string(b), `"./marketplace/plugins/p"`) || !strings.Contains(string(b), `"./x"`) {
		t.Fatalf("manifest = %s", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "marketplace")); err != nil {
		t.Fatal(err)
	}
}

func TestSettings(t *testing.T) {
	b, err := Settings([]string{"a@m"}, map[string]string{"m": "/d"})
	if err != nil || !strings.Contains(string(b), `"a@m": true`) || !strings.Contains(string(b), `"path": "/d"`) {
		t.Fatalf("%s %v", b, err)
	}
}

// Two plugin folders of one name (a worktree's copy and another checkout's) are installed once, the first
// one winning: linking both would fail with "symlink ...: file exists".
func TestLocalPluginMarketplaceInstallsOneNameOnce(t *testing.T) {
	a, b, dir := t.TempDir(), t.TempDir(), t.TempDir()
	for _, p := range []string{a, b} {
		if err := os.MkdirAll(filepath.Join(p, ".claude-plugin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, ".claude-plugin", "plugin.json"), []byte(`{"name":"sloprail"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	names, err := LocalPluginMarketplace("m", dir, []string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "sloprail" {
		t.Fatalf("names = %v", names)
	}
	got, err := os.Readlink(filepath.Join(dir, "plugins", "sloprail"))
	if err != nil || got != a {
		t.Fatalf("link = %q, %v; want %q", got, err, a)
	}
}
