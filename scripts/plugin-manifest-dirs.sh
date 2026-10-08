# Sourced, not run: the folders where each harness keeps a plugin's plugin.json.
# Shell twin of internal/harness/pluginmanifest.go.
plugin_manifest_dirs=".claude-plugin .codex-plugin .cursor-plugin"

# plugin_manifests <root> -> every harness's plugin.json below <root>/marketplace/plugins.
plugin_manifests() {
  local d f
  for d in $plugin_manifest_dirs; do
    for f in "$1"/marketplace/plugins/*/"$d"/plugin.json; do
      [ -f "$f" ] && printf '%s\n' "$f"
    done
  done
  return 0
}
