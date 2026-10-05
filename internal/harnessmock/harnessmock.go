// Package harnessmock locates the pinned a10n-claude-mock (a stand-in `claude`)
// and builds the plugin wiring a mock session needs: a directory-sourced local
// marketplace and the settings that enable sloprail from it. sr-test and the
// e2e harness share it.
package harnessmock

import (
	"debug/buildinfo"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

//go:embed version.txt
var versionFile string

// Version is the pinned a10n-claude-mock version (also tests/e2e/harness/MOCK_VERSION).
var Version = strings.TrimSpace(versionFile)

const modulePath = "github.com/sloprail/harness-mocks"

// Path finds a10n-claude-mock: $A10N_CLAUDE_MOCK, else PATH. The binary has no
// --version flag, so its version is read from the Go build info it carries.
func Path() (string, error) {
	p := os.Getenv("A10N_CLAUDE_MOCK")
	if p == "" {
		var err error
		if p, err = exec.LookPath("a10n-claude-mock"); err != nil {
			return "", fmt.Errorf("a10n-claude-mock %s not found: set A10N_CLAUDE_MOCK or put it on PATH (make mock, or: go install %s/claude-mock@%s and rename claude-mock to a10n-claude-mock)", Version, modulePath, Version)
		}
	}
	got, err := versionOf(p)
	if err != nil {
		return "", fmt.Errorf("a10n-claude-mock at %s: cannot read its version (%v); expected %s", p, err, Version)
	}
	if got != Version {
		return "", fmt.Errorf("a10n-claude-mock at %s is %s, expected %s: install it with make mock, or go install %s/claude-mock@%s", p, got, Version, modulePath, Version)
	}
	return p, nil
}

func versionOf(path string) (string, error) {
	bi, err := buildinfo.ReadFile(path)
	if err != nil {
		return "", err
	}
	if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version, nil
	}
	return "", fmt.Errorf("no module version in build info (path %s)", bi.Path)
}

// LocalMarketplace writes into dir a copy of the checkout's marketplace
// manifest with every plugin source made local, beside a symlink to the
// checkout's marketplace/. The shipped .claude-plugin/marketplace.json pins
// plugins to release tags; a directory-sourced marketplace must resolve them to
// this working tree, and the mock reads only relative-path sources.
func LocalMarketplace(repoRoot, dir string) error {
	raw, err := os.ReadFile(filepath.Join(repoRoot, ".claude-plugin", "marketplace.json"))
	if err != nil {
		return fmt.Errorf("read marketplace.json: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse marketplace.json: %w", err)
	}
	plugins, _ := doc["plugins"].([]any)
	for _, p := range plugins {
		pm, _ := p.(map[string]any)
		if _, isString := pm["source"].(string); isString {
			continue
		}
		pm["source"] = "./marketplace/plugins/" + fmt.Sprint(pm["name"])
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode marketplace.json: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude-plugin", "marketplace.json"), body, 0o644); err != nil {
		return err
	}
	return os.Symlink(filepath.Join(repoRoot, "marketplace"), filepath.Join(dir, "marketplace"))
}

// Settings is the settings.json body that enables each "<plugin>@<marketplace>"
// key in enabled, with marketplaces mapping a marketplace name to the directory
// it is sourced from.
func Settings(enabled []string, marketplaces map[string]string) ([]byte, error) {
	en := map[string]any{}
	for _, k := range enabled {
		en[k] = true
	}
	mk := map[string]any{}
	for name, dir := range marketplaces {
		mk[name] = map[string]any{"source": map[string]any{"source": "directory", "path": dir}}
	}
	return json.MarshalIndent(map[string]any{"enabledPlugins": en, "extraKnownMarketplaces": mk}, "", "  ")
}

// LocalPluginMarketplace writes into dir a marketplace named marketplace whose plugins are the given
// local plugin folders (each linked under plugins/<name>); a plugin's name is its plugin.json name,
// else its folder name. It returns the plugin names in order.
func LocalPluginMarketplace(marketplace, dir string, pluginDirs []string) ([]string, error) {
	var names []string
	var entries []any
	for _, pd := range pluginDirs {
		name := filepath.Base(pd)
		if raw, err := os.ReadFile(filepath.Join(pd, ".claude-plugin", "plugin.json")); err == nil {
			var m struct{ Name string }
			if json.Unmarshal(raw, &m) == nil && m.Name != "" {
				name = m.Name
			}
		}
		if slices.Contains(names, name) {
			continue // one name is installed once: the first folder wins
		}
		if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
			return nil, err
		}
		if err := os.Symlink(pd, filepath.Join(dir, "plugins", name)); err != nil {
			return nil, err
		}
		names = append(names, name)
		entries = append(entries, map[string]any{"name": name, "source": "./plugins/" + name})
	}
	body, err := json.MarshalIndent(map[string]any{"name": marketplace, "owner": map[string]any{"name": "sr-test"}, "plugins": entries}, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o755); err != nil {
		return nil, err
	}
	return names, os.WriteFile(filepath.Join(dir, ".claude-plugin", "marketplace.json"), body, 0o644)
}
