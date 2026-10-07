package cursor

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/sloprail/sloprail/internal/harness"
)

// localMarketplace is the marketplace name a plugin found by directory is filed
// under: Cursor's plugins have no `<plugin>@<marketplace>` key to split.
const localMarketplace = "local"

// Resolve locates the plugins Cursor loads from disk for a project.
//
// Cursor has no per-project "enabled plugins" setting a hook can read (the
// recordings load plugins only through `--plugin-dir`, a launch flag no hook
// payload or variable reports), and the account's marketplace installs are managed
// in its dashboard. What is on disk and discoverable is a plugin directory
// carrying .cursor-plugin/plugin.json under <home>/.cursor/plugins/local/ (Cursor's
// documented place for a local plugin: https://cursor.com/docs/plugins; the path is
// not exercised by any harness-mocks recording). Each such directory resolves; one
// with no readable manifest is Unresolved, naming what was tried, so a half-installed
// plugin is reported rather than read as "no plugins".
//
// A plugin loaded with --plugin-dir from elsewhere is NOT discovered: nothing in
// the hook's input names its directory. Said here rather than papered over.
func Resolve(projectDir, home string) (harness.Resolution, error) {
	var res harness.Resolution
	root := filepath.Join(home, ".cursor", "plugins", "local")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return res, nil
		}
		return res, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			continue // follows symlinks: a linked plugin is the usual local install
		}
		plugin := harness.Plugin{Name: e.Name(), Marketplace: localMarketplace}
		manifest := filepath.Join(dir, ".cursor-plugin", "plugin.json")
		if _, err := os.Stat(manifest); err != nil {
			res.Unresolved = append(res.Unresolved, harness.Unresolved{
				Plugin: plugin, Key: plugin.Key(), Tried: []string{manifest},
				Reason: "the directory has no .cursor-plugin/plugin.json",
			})
			continue
		}
		res.Roots = append(res.Roots, harness.Root{Plugin: plugin, Dir: dir})
	}
	return res, nil
}
