package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/sloprail/sloprail/internal/harness"
)

// This file resolves which plugins a PROJECT has installed in Codex, by reading
// Codex's own configuration — the same argument claudecode/plugins.go makes for why
// the engine reads a harness's settings (and why an enabled plugin that cannot be
// found is reported as Unresolved, never skipped).
//
// What is assumed about Codex, each stated here and nowhere else:
//
//  1. config.toml in the user layer ($CODEX_HOME, default ~/.codex) and the project
//     layer (<project>/.codex) holds `[plugins."<plugin>@<marketplace>"]` tables with
//     `enabled = true|false`. The project layer wins on a key both name.
//  2. `[marketplaces.<name>]` tables declare a marketplace, `source` being the
//     directory of a local one.
//  3. A local marketplace lists its plugins in `.agents/plugins/marketplace.json`:
//     {"plugins": [{"name", "source": {"source": "local", "path": "./plugins/<name>"}}]}.
//  4. Otherwise `codex plugin add` has installed the plugin into the cache,
//     `<CODEX_HOME>/plugins/cache/<marketplace>/<plugin>/<version>/`.
//
// Documented at https://developers.openai.com/plugins/build/plugins and recorded in
// harness-mocks codex-mock/snapshots/runs/plugin-hooks (1 and 2 from its
// config.toml writes, 3 from its marketplace.json). 4 is the one assumption no
// recording covers; a wrong guess there surfaces as Unresolved.

type config struct {
	Marketplaces map[string]struct {
		Source string `toml:"source"`
	} `toml:"marketplaces"`
	Plugins map[string]struct {
		Enabled *bool `toml:"enabled"`
	} `toml:"plugins"`
}

// ResolvePlugins implements harness.Harness.
func (Harness) ResolvePlugins(projectDir, home string) (harness.Resolution, error) {
	return Resolve(projectDir, home)
}

// codexHome is Codex's home for a given user home: $CODEX_HOME wins.
func codexHome(home string) string {
	if v := strings.TrimSpace(os.Getenv("CODEX_HOME")); v != "" {
		return v
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".codex")
}

// Resolve reads the enabled plugins and locates each.
func Resolve(projectDir, home string) (harness.Resolution, error) {
	var res harness.Resolution
	chome := codexHome(home)

	enabled := map[string]bool{}
	var order []string
	marketplaces := map[string]string{}
	layers := []string{}
	if chome != "" {
		layers = append(layers, filepath.Join(chome, "config.toml"))
	}
	if projectDir != "" {
		layers = append(layers, filepath.Join(projectDir, ".codex", "config.toml"))
	}
	for _, path := range layers {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return res, fmt.Errorf("codex: read %s: %w", path, err)
		}
		var c config
		if err := toml.Unmarshal(data, &c); err != nil {
			return res, fmt.Errorf("codex: parse %s: %w", path, err)
		}
		for name, m := range c.Marketplaces {
			if m.Source != "" {
				marketplaces[name] = m.Source
			}
		}
		for key, p := range c.Plugins {
			if _, seen := enabled[key]; !seen {
				order = append(order, key)
			}
			// Absent `enabled` is Codex's default, on.
			enabled[key] = p.Enabled == nil || *p.Enabled
		}
	}
	sort.Strings(order)

	for _, key := range order {
		if !enabled[key] {
			continue
		}
		pl, ok := parseKey(key)
		if !ok {
			res.Unresolved = append(res.Unresolved, harness.Unresolved{Key: key, Reason: "the key is not <plugin>@<marketplace>"})
			continue
		}
		dir, tried := locate(pl, marketplaces, chome)
		if dir == "" {
			res.Unresolved = append(res.Unresolved, harness.Unresolved{
				Plugin: pl, Key: key, Tried: tried,
				Reason: "no marketplace source or cache entry holds it",
			})
			continue
		}
		res.Roots = append(res.Roots, harness.Root{Plugin: pl, Dir: dir})
	}
	return res, nil
}

func parseKey(key string) (harness.Plugin, bool) {
	name, mk, ok := strings.Cut(key, "@")
	if !ok || name == "" || mk == "" || !safeName(name) || !safeName(mk) {
		return harness.Plugin{}, false
	}
	return harness.Plugin{Name: name, Marketplace: mk}, true
}

// safeName refuses a name that could leave the directory it is joined onto.
func safeName(s string) bool {
	return s != "." && s != ".." && !strings.ContainsAny(s, `/\`)
}

// locate finds a plugin's directory: through its declared local marketplace, else in
// the cache. tried lists what was looked at.
func locate(p harness.Plugin, marketplaces map[string]string, chome string) (string, []string) {
	var tried []string
	if src, ok := marketplaces[p.Marketplace]; ok {
		manifest := filepath.Join(src, ".agents", "plugins", "marketplace.json")
		tried = append(tried, manifest)
		if dir := fromMarketplace(src, manifest, p.Name); dir != "" && isDir(dir) {
			return dir, tried
		}
	}
	if chome != "" {
		cache := filepath.Join(chome, "plugins", "cache", p.Marketplace, p.Name)
		tried = append(tried, cache)
		if versions, err := os.ReadDir(cache); err == nil {
			var dirs []string
			for _, v := range versions {
				if v.IsDir() {
					dirs = append(dirs, v.Name())
				}
			}
			sort.Strings(dirs)
			if len(dirs) > 0 {
				return filepath.Join(cache, dirs[len(dirs)-1]), tried
			}
		}
	}
	return "", tried
}

func fromMarketplace(root, manifest, name string) string {
	data, err := os.ReadFile(manifest)
	if err != nil {
		return ""
	}
	var m struct {
		Plugins []struct {
			Name   string `json:"name"`
			Source struct {
				Path string `json:"path"`
			} `json:"source"`
		} `json:"plugins"`
	}
	if json.Unmarshal(data, &m) != nil {
		return ""
	}
	for _, p := range m.Plugins {
		if p.Name == name && p.Source.Path != "" {
			return filepath.Join(root, p.Source.Path)
		}
	}
	return ""
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
