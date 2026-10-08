package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// InstalledPluginSkill returns the path of the plugin-under-test's SKILL.md for skill
// name in the copy the selected harness INSTALLS and loads from, which is not always the
// directory the engine resolves the plugin to: Codex and Claude Code unpack an installed
// plugin into a cache while a local marketplace resolves to its source.
//
//   - Codex: the mock installs the plugin at run time into
//     `$CODEX_HOME/plugins/cache/<marketplace>/<plugin>/<version>` (version from the
//     plugin's manifest); the path is returned, the copy appears when the run starts.
//   - Claude Code: a directory marketplace leaves the cache empty, so the copy is made here
//     in `<plugin cache dir>/<marketplace>/<plugin>/<version>`.
//   - Cursor loads the plugin from its --plugin-dir, the source, so that is its copy.
func (e *Env) InstalledPluginSkill(name string) string {
	e.t.Helper()
	src := filepath.Join(e.repoRoot, "marketplace", "plugins", pluginName)
	switch Selected(e.t) {
	case "codex":
		raw, err := os.ReadFile(filepath.Join(src, ".codex-plugin", "plugin.json"))
		if err != nil {
			e.t.Fatalf("harness: read the codex manifest: %v", err)
		}
		var m struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(raw, &m) != nil || m.Version == "" {
			e.t.Fatalf("harness: the codex manifest names no version")
		}
		return filepath.Join(e.configDir, "plugins", "cache", marketplaceName, pluginName, m.Version, "skills", name, "SKILL.md")
	case "claude":
		body, err := os.ReadFile(filepath.Join(src, "skills", name, "SKILL.md"))
		if err != nil {
			e.t.Fatalf("harness: read the plugin's %s skill: %v", name, err)
		}
		path := filepath.Join(e.pluginDir, marketplaceName, pluginName, "0.0.0-e2e", "skills", name, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			e.t.Fatalf("harness: %v", err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			e.t.Fatalf("harness: %v", err)
		}
		return path
	default:
		return filepath.Join(src, "skills", name, "SKILL.md")
	}
}
