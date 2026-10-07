package cursor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sloprail/sloprail/internal/harness"
)

// localMarketplace is the marketplace name a plugin found by directory is filed
// under: Cursor's plugins have no `<plugin>@<marketplace>` key to split.
const localMarketplace = "local"

// PluginRootEnv is set by cursor-agent in every hook's environment to the directory of
// the plugin that hook belongs to (measured with `--plugin-dir`, cursor-agent
// 2026.10.01, beside CLAUDE_PLUGIN_ROOT with the same value), and a plugin hook's
// working directory is that same directory. So a hook finds its own plugin from the
// environment or from where it runs, with no search.
const PluginRootEnv = "CURSOR_PLUGIN_ROOT"

// Resolve locates the plugins a Cursor project has loaded that a hook can know of:
//
//   - the plugin whose hook is running (CURSOR_PLUGIN_ROOT), however it was loaded
//     (`--plugin-dir`, the account's marketplace);
//   - the plugin the project's own hooks run: the install of sloprail's stop and
//     sessionStart hooks (project-hooks install) names the plugin's script in
//     <project>/.cursor/hooks.json, the one record, available to a command the agent
//     runs in its shell and to the user's terminal, of where that plugin is;
//   - the user's local plugins, <home>/.cursor/plugins/local/<name>, which Cursor
//     loads at start (read off the cursor-agent binary: a directory or a symlink whose
//     target stays inside that directory; dot-names skipped).
//
// What it cannot know, said rather than papered over: the account's marketplace
// installs are cached under <home>/.cursor/plugins/cache/<marketplace>/<plugin>/<version>
// but whether each is ENABLED is account state no hook input names, so a cached plugin
// is not assumed enabled, and a plugin loaded with --plugin-dir is found only from its
// own hook. A directory with no readable manifest is Unresolved, naming what was
// tried, so a half-installed plugin is reported rather than read as "no plugins".
func Resolve(projectDir, home string) (harness.Resolution, error) {
	var res harness.Resolution
	seen := map[string]bool{}
	add := func(name, dir string) {
		real := dir
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			real = r
		}
		if seen[real] {
			return
		}
		seen[real] = true
		plugin := harness.Plugin{Name: name, Marketplace: localMarketplace}
		manifest := filepath.Join(dir, ".cursor-plugin", "plugin.json")
		raw, err := os.ReadFile(manifest)
		if err != nil {
			res.Unresolved = append(res.Unresolved, harness.Unresolved{
				Plugin: plugin, Key: plugin.Key(), Tried: []string{manifest},
				Reason: "the directory has no readable .cursor-plugin/plugin.json",
			})
			return
		}
		var m struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &m) == nil && m.Name != "" {
			plugin.Name = m.Name
		}
		res.Roots = append(res.Roots, harness.Root{Plugin: plugin, Dir: dir})
	}

	if root := os.Getenv(PluginRootEnv); root != "" {
		add(filepath.Base(root), root)
	}

	for _, dir := range projectHookPlugins(projectDir) {
		add(filepath.Base(dir), dir)
	}

	local := filepath.Join(home, ".cursor", "plugins", "local")
	entries, err := os.ReadDir(local)
	if err != nil {
		if os.IsNotExist(err) {
			return res, nil
		}
		return res, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	base, _ := filepath.EvalSymlinks(local)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(local, e.Name())
		target, err := filepath.EvalSymlinks(dir)
		if err != nil {
			continue
		}
		if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
			continue
		}
		if rel, err := filepath.Rel(base, target); err != nil || strings.HasPrefix(rel, "..") {
			continue // Cursor rejects a link pointing outside the local directory
		}
		add(e.Name(), dir)
	}
	return res, nil
}

// projectHookPlugins are the plugin directories whose sloprail hook script the project's
// .cursor/hooks.json runs (see InstallProjectHooks). A missing or unparsable hooks file
// names none: it is the user's file, and whoever edits it reports a broken one.
func projectHookPlugins(project string) []string {
	raw, err := os.ReadFile(HooksPath(project))
	if err != nil {
		return nil
	}
	var doc struct {
		Hooks map[string][]struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	events := make([]string, 0, len(doc.Hooks))
	for ev := range doc.Hooks {
		events = append(events, ev)
	}
	sort.Strings(events)
	var dirs []string
	for _, ev := range events {
		for _, h := range doc.Hooks[ev] {
			i := strings.Index(h.Command, hookScript)
			if i < 0 {
				continue
			}
			script := h.Command[:i+len(hookScript)]
			if strings.HasPrefix(script, "'") { // shellWord's quoting
				script = strings.ReplaceAll(strings.TrimPrefix(script, "'"), `'\''`, "'")
			}
			dirs = append(dirs, filepath.Dir(filepath.Dir(script)))
		}
	}
	return dirs
}
