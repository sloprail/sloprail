package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPluginVersionReadsAnyHarnessManifest(t *testing.T) {
	for _, d := range []string{".claude-plugin", ".codex-plugin", ".cursor-plugin"} {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, d, "plugin.json"), []byte(`{"name":"p","version":"1.2.3"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := pluginVersion(dir); got != "1.2.3" {
			t.Errorf("%s: version = %q, want 1.2.3", d, got)
		}
	}
	if got := pluginVersion(t.TempDir()); got != "" {
		t.Errorf("no manifest: version = %q, want empty", got)
	}
}
