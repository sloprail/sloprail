package srtest

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Context is what a case runs in: the temp working directory and, for a plugin case, the plugin
// folders installed into it (the plugin under test first, then the core sloprail plugin).
type Context struct {
	Dir     string
	Plugins []string
}

// Case is one .sloprail/tests/<name>/test.sh and the context it belongs to.
//
// A repo can hold .sloprail/ folders below the root. A case runs in the context of the .sloprail/
// it sits in:
//   - root (or any non-plugin folder): the .sloprail/ is copied into the case's fresh temp dir;
//   - plugin (the .sloprail/'s parent holds .claude-plugin/plugin.json): that plugin folder is
//     installed from its local checkout as the plugin under test, beside the core sloprail plugin;
//     the host repo's root .sloprail/ is NOT copied, and the case's temp dir starts empty.
type Case struct {
	Name        string   // folder name of the case
	Subject     string   // the result's subject: "<name>" for the root, "<rel dir of the .sloprail's parent>:<name>" below it
	Dir         string   // absolute source path of the case folder
	SloprailDir string   // the .sloprail/ it belongs to
	Target      string   // the folder whose markers pick the harness (the .sloprail's parent)
	Plugins     []string // plugin case: plugin folders to install (empty for a project case)
}

// skipDirs are never searched: dependencies, VCS data, and the temp / worktree folders.
var skipDirs = map[string]bool{"node_modules": true, ".git": true, ".claude": true, ".worktrees": true}

// IsPlugin reports whether dir is a Claude plugin folder.
func IsPlugin(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	return err == nil
}

// Discover finds every **/.sloprail/tests/*/test.sh below root. Order: the root's cases, then the
// rest by subject. corePlugin (may be "") is the core sloprail plugin folder added to a plugin case
// unless that case's plugin is itself.
func Discover(root, corePlugin string) ([]Case, error) {
	var out []Case
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if p != root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), "sr-test-")) {
			return filepath.SkipDir
		}
		if d.Name() != ".sloprail" {
			return nil
		}
		out = append(out, casesIn(root, p, corePlugin)...)
		return filepath.SkipDir
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := out[i].Target == root, out[j].Target == root
		if ri != rj {
			return ri
		}
		return out[i].Subject < out[j].Subject
	})
	return out, nil
}

func casesIn(root, sloprail, corePlugin string) []Case {
	ents, err := os.ReadDir(filepath.Join(sloprail, "tests"))
	if err != nil {
		return nil
	}
	owner := filepath.Dir(sloprail)
	var plugins []string
	if IsPlugin(owner) {
		plugins = []string{owner}
		if corePlugin != "" && !sameDir(owner, corePlugin) {
			plugins = append(plugins, corePlugin)
		}
	}
	prefix := ""
	if owner != root {
		rel, _ := filepath.Rel(root, owner)
		prefix = filepath.ToSlash(rel) + ":"
	}
	var out []Case
	for _, e := range ents {
		dir := filepath.Join(sloprail, "tests", e.Name())
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "test.sh")); err != nil {
			continue
		}
		out = append(out, Case{Name: e.Name(), Subject: prefix + e.Name(), Dir: dir, SloprailDir: sloprail, Target: owner, Plugins: plugins})
	}
	return out
}

func sameDir(a, b string) bool {
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	return a == b
}
