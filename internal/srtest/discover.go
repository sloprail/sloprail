package srtest

import (
	"encoding/json"
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

// Nature of a case's owner. A case proves ONE rule: the folder it sits in.
const (
	NatureGate      = "gate"
	NatureFileGuard = "file-guard"
	NatureContext   = "context"
	NatureStructure = "structure" // the structure gate, .sloprail/file-guard/structure.yaml
)

// structureTests is the folder the structure gate (one file, not a folder) owns for its cases.
const structureTests = "structure.tests"

var ruleNatures = []string{NatureGate, NatureFileGuard, NatureContext}

// Case is one test.sh and the context it belongs to. It sits in its OWNING rule's folder:
//
//	.sloprail/<gate|file-guard|context>/<rule>/tests/<case>/test.sh
//	.sloprail/file-guard/structure.tests/<case>/test.sh     (the structure gate)
//
// A repo can hold .sloprail/ folders below the root. A case runs in the context of the .sloprail/
// it sits in:
//   - root (or any non-plugin folder): the .sloprail/ is copied into the case's fresh temp dir;
//   - plugin (the .sloprail/'s parent holds .claude-plugin/plugin.json): that plugin folder is
//     installed from its local checkout as the plugin under test, beside the core sloprail plugin;
//     the host repo's root .sloprail/ is NOT copied, and the case's temp dir starts empty.
type Case struct {
	Name        string   // folder name of the case
	Nature      string   // owner's nature: gate, file-guard, context or structure
	Rule        string   // owner's rule name ("" for the structure gate)
	Subject     string   // the result's subject: "<owner>:<name>" for the root, "<rel dir of the .sloprail's parent>:<owner>:<name>" below it
	Dir         string   // absolute source path of the case folder
	SloprailDir string   // the .sloprail/ it belongs to
	Target      string   // the folder whose markers pick the harness (the .sloprail's parent)
	Plugins     []string // plugin case: plugin folders to install (empty for a project case)
}

// Owner is the owning rule within its .sloprail/: "gate/<rule>", "file-guard/<rule>",
// "context/<rule>" or "file-guard/structure".
func (c Case) Owner() string {
	if c.Nature == NatureStructure {
		return NatureFileGuard + "/structure"
	}
	return c.Nature + "/" + c.Rule
}

// skipDirs are never searched: dependencies, VCS data, and the temp / worktree folders.
var skipDirs = map[string]bool{"node_modules": true, ".git": true, ".claude": true, ".worktrees": true}

// IsPlugin reports whether dir is a Claude plugin folder.
func IsPlugin(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	return err == nil
}

// sloprailDirs lists every .sloprail/ folder below root (not descending into one, nor into skipDirs).
func sloprailDirs(root string) ([]string, error) {
	var out []string
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
		out = append(out, p)
		return filepath.SkipDir
	})
	return out, err
}

// Discover finds every case below root: **/.sloprail/<nature>/<rule>/tests/<case>/test.sh and
// **/.sloprail/file-guard/structure.tests/<case>/test.sh. Order: the root's cases, then the rest
// by subject. corePlugin (may be "") is the core sloprail plugin folder added to a plugin case
// unless that case's plugin is itself.
func Discover(root, corePlugin string) ([]Case, error) {
	dirs, err := sloprailDirs(root)
	if err != nil {
		return nil, err
	}
	var out []Case
	for _, d := range dirs {
		out = append(out, casesIn(root, d, corePlugin)...)
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

// subDirs lists the names of the subfolders of dir (none when it cannot be read).
func subDirs(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

func casesIn(root, sloprail, corePlugin string) []Case {
	owner := filepath.Dir(sloprail)
	var plugins []string
	if IsPlugin(owner) {
		plugins = []string{owner}
		// The plugin under test is the core plugin when it is the same folder, or another
		// checkout's copy of it (a git worktree beside the checkout the binary was built in):
		// one name is installed once, and the one under test wins.
		if corePlugin != "" && !sameDir(owner, corePlugin) && PluginName(owner) != PluginName(corePlugin) {
			plugins = append(plugins, corePlugin)
		}
	}
	prefix := ""
	if owner != root {
		rel, _ := filepath.Rel(root, owner)
		prefix = filepath.ToSlash(rel) + ":"
	}
	var out []Case
	add := func(nature, rule, testsDir string) {
		for _, name := range subDirs(testsDir) {
			dir := filepath.Join(testsDir, name)
			if _, err := os.Stat(filepath.Join(dir, "test.sh")); err != nil {
				continue
			}
			c := Case{Name: name, Nature: nature, Rule: rule, Dir: dir, SloprailDir: sloprail, Target: owner, Plugins: plugins}
			c.Subject = prefix + c.Owner() + ":" + name
			out = append(out, c)
		}
	}
	for _, nature := range ruleNatures {
		for _, rule := range subDirs(filepath.Join(sloprail, nature)) {
			if nature == NatureFileGuard && rule == structureTests {
				continue
			}
			add(nature, rule, filepath.Join(sloprail, nature, rule, "tests"))
		}
	}
	add(NatureStructure, "", filepath.Join(sloprail, NatureFileGuard, structureTests))
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

// PluginName is a plugin folder's name: plugin.json's "name", else the folder's base name.
func PluginName(dir string) string {
	if raw, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json")); err == nil {
		var m struct{ Name string }
		if json.Unmarshal(raw, &m) == nil && m.Name != "" {
			return m.Name
		}
	}
	return filepath.Base(dir)
}
