package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shipped manifest pins plugins to a release tag (git-subdir). The snapshot
// an eval installs from must point at the checkout's own copy instead, or every
// eval silently tests the released rules.
func TestSnapshotMarketplace_PluginSourcesAreLocalPaths(t *testing.T) {
	repo := t.TempDir()
	writeIn(t, filepath.Join(repo, ".claude-plugin", "marketplace.json"), `{"name":"m","plugins":[
{"name":"a","version":"1.0.0","source":{"source":"git-subdir","url":"o/r","path":"marketplace/plugins/a","ref":"v1.0.0"}},
{"name":"b","source":"./marketplace/plugins/b"}]}`)
	writeIn(t, filepath.Join(repo, "marketplace", "plugins", "a", ".claude-plugin", "plugin.json"), "{}\n")
	writeIn(t, filepath.Join(repo, "marketplace", "plugins", "b", ".claude-plugin", "plugin.json"), "{}\n")

	w := &workspace{root: t.TempDir()}
	dst, err := w.snapshotMarketplace(repo)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dst, ".claude-plugin", "marketplace.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "git-subdir") {
		t.Errorf("snapshot manifest still has a git-subdir source:\n%s", raw)
	}
	var m struct {
		Plugins []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Source  string `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("source is not a plain string: %v\n%s", err, raw)
	}
	if len(m.Plugins) != 2 || m.Plugins[0].Version != "1.0.0" {
		t.Fatalf("other plugin fields lost: %+v", m.Plugins)
	}
	for _, p := range m.Plugins {
		if want := "./marketplace/plugins/" + p.Name; p.Source != want {
			t.Errorf("%s: source %q, want %q", p.Name, p.Source, want)
		}
		if _, err := os.Stat(filepath.Join(dst, p.Source, ".claude-plugin", "plugin.json")); err != nil {
			t.Errorf("%s: source does not exist in the snapshot: %v", p.Name, err)
		}
	}
}

// The same holds for this repo's real manifest, whatever it pins.
func TestSnapshotMarketplace_RealManifestHasNoGitSubdir(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	w := &workspace{root: t.TempDir()}
	dst, err := w.snapshotMarketplace(repo)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dst, ".claude-plugin", "marketplace.json"))
	if strings.Contains(string(raw), "git-subdir") {
		t.Errorf("real snapshot still pinned:\n%s", raw)
	}
	var m struct {
		Plugins []struct {
			Source string `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &m); err != nil || len(m.Plugins) == 0 {
		t.Fatalf("parse: %v", err)
	}
	for _, p := range m.Plugins {
		if _, err := os.Stat(filepath.Join(dst, p.Source, ".claude-plugin", "plugin.json")); err != nil {
			t.Errorf("%q missing in snapshot: %v", p.Source, err)
		}
	}
}
