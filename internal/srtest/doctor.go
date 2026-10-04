package srtest

import (
	"os"
	"path/filepath"
	"sort"
)

// declFiles is the declaration file each nature's rule folder holds.
var declFiles = map[string]string{NatureGate: "gate.yaml", NatureFileGuard: "file-guard.yaml", NatureContext: "context.yaml"}

// UncoveredRule is a rule with no case, located: which .sloprail/ it sits in (Dir, the
// slash-separated path of that .sloprail's parent relative to the scanned root, "." for the
// root's own) and its qualified name as Uncovered prints it.
type UncoveredRule struct {
	Nature    string `json:"nature"`    // "gate", "file-guard", "context", or "structure" for the structure gate
	Rule      string `json:"rule"`      // the rule's folder name ("structure" for the structure gate)
	Dir       string `json:"dir"`       // the .sloprail's parent relative to the root, "." for the root's own
	Qualified string `json:"qualified"` // "<rule>", "<plugin>/<rule>" or "<dir>/<rule>", as in Uncovered
}

// Uncovered returns the rules below root that have ZERO cases, as "<nature>:<rule>": a rule folder
// holding a declaration whose tests/ holds no <case>/test.sh, and the structure gate
// (.sloprail/file-guard/structure.yaml) whose structure.tests/ holds none ("structure:structure").
// A rule in a plugin's .sloprail/ is qualified "<plugin>/<rule>" (the plugin's name, else its
// folder's), one in another nested .sloprail/ "<dir>/<rule>" (its directory relative to root).
// Purely structural: nothing is run, and ownership is the folder, not an observed event.
func Uncovered(root string) ([]string, error) {
	rules, err := UncoveredRules(root)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.Nature+":"+r.Qualified)
	}
	sort.Strings(out)
	return out, nil
}

// UncoveredRules is Uncovered with each rule located, so a consumer matches on the .sloprail it
// came from (Dir) and the rule (Nature, Rule) instead of rebuilding the qualified name.
func UncoveredRules(root string) ([]UncoveredRule, error) {
	dirs, err := sloprailDirs(root)
	if err != nil {
		return nil, err
	}
	var out []UncoveredRule
	for _, d := range dirs {
		out = append(out, uncoveredIn(root, d)...)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Nature+":"+out[i].Qualified, out[j].Nature+":"+out[j].Qualified; a != b {
			return a < b
		}
		return out[i].Dir < out[j].Dir
	})
	return out, nil
}

func uncoveredIn(root, sloprail string) []UncoveredRule {
	parent := filepath.Dir(sloprail)
	dir := "."
	if parent != root {
		rel, _ := filepath.Rel(root, parent)
		dir = filepath.ToSlash(rel)
	}
	qual := func(name string) string {
		switch {
		case IsPlugin(parent):
			return PluginName(parent) + "/" + name
		case parent != root:
			return dir + "/" + name
		}
		return name
	}
	has := map[string]bool{}
	for _, c := range casesIn(root, sloprail, "") {
		has[c.Owner()] = true
	}
	var out []UncoveredRule
	for _, nature := range ruleNatures {
		for _, rule := range subDirs(filepath.Join(sloprail, nature)) {
			if nature == NatureFileGuard && rule == structureTests {
				continue
			}
			if _, err := os.Stat(filepath.Join(sloprail, nature, rule, declFiles[nature])); err != nil {
				continue
			}
			if !has[nature+"/"+rule] {
				out = append(out, UncoveredRule{Nature: nature, Rule: rule, Dir: dir, Qualified: qual(rule)})
			}
		}
	}
	if _, err := os.Stat(filepath.Join(sloprail, NatureFileGuard, "structure.yaml")); err == nil && !has[NatureFileGuard+"/structure"] {
		out = append(out, UncoveredRule{Nature: "structure", Rule: "structure", Dir: dir, Qualified: qual("structure")})
	}
	return out
}
