package harness

import "path/filepath"

// PluginManifestDirs are the folders, inside a plugin, where each harness keeps its
// plugin.json. The one list every Go caller reads; scripts/plugin-manifest-dirs.sh is
// its shell twin.
var PluginManifestDirs = []string{".claude-plugin", ".codex-plugin", ".cursor-plugin"}

// PluginManifestPaths are the plugin.json paths a plugin folder dir may hold, one per
// harness, in PluginManifestDirs order.
func PluginManifestPaths(dir string) []string {
	out := make([]string, 0, len(PluginManifestDirs))
	for _, d := range PluginManifestDirs {
		out = append(out, filepath.Join(dir, d, "plugin.json"))
	}
	return out
}
