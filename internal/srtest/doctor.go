package srtest

import (
	"os"
	"path/filepath"
	"sort"
)

// declFiles is the declaration file each nature's rule folder holds.
var declFiles = map[string]string{NatureGate: "gate.yaml", NatureFileGuard: "file-guard.yaml", NatureContext: "context.yaml"}

// Uncovered returns the rules below root that have ZERO cases, as "<nature>:<rule>": a rule folder
// holding a declaration whose tests/ holds no <case>/test.sh, and the structure gate
// (.sloprail/file-guard/structure.yaml) whose structure.tests/ holds none ("structure:structure").
// A rule in a plugin's .sloprail/ is qualified "<plugin>/<rule>" (the plugin's name), one in
// another nested .sloprail/ "<dir>/<rule>" (its directory relative to root). Purely structural:
// nothing is run, and ownership is the folder, not an observed event.
func Uncovered(root string) ([]string, error) {
	dirs, err := sloprailDirs(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range dirs {
		out = append(out, uncoveredIn(root, d)...)
	}
	sort.Strings(out)
	return out, nil
}

func uncoveredIn(root, sloprail string) []string {
	parent := filepath.Dir(sloprail)
	qual := func(name string) string {
		switch {
		case IsPlugin(parent):
			return PluginName(parent) + "/" + name
		case parent != root:
			rel, _ := filepath.Rel(root, parent)
			return filepath.ToSlash(rel) + "/" + name
		}
		return name
	}
	has := map[string]bool{}
	for _, c := range casesIn(root, sloprail, "") {
		has[c.Owner()] = true
	}
	var out []string
	for _, nature := range ruleNatures {
		for _, rule := range subDirs(filepath.Join(sloprail, nature)) {
			if nature == NatureFileGuard && rule == structureTests {
				continue
			}
			if _, err := os.Stat(filepath.Join(sloprail, nature, rule, declFiles[nature])); err != nil {
				continue
			}
			if !has[nature+"/"+rule] {
				out = append(out, nature+":"+qual(rule))
			}
		}
	}
	if _, err := os.Stat(filepath.Join(sloprail, NatureFileGuard, "structure.yaml")); err == nil && !has[NatureFileGuard+"/structure"] {
		out = append(out, "structure:"+qual("structure"))
	}
	return out
}
