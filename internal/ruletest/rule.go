package ruletest

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/module/modules"
)

// Rule is one declaration of a `.sloprail` tree, as the tests see it.
type Rule struct {
	Nature declaration.Nature
	Name   string
	// Dir is the rule's folder; Root the `.sloprail` it sits in; Plugin the plugin
	// it ships in ("" for a project's own rule).
	Dir, Root, Plugin string
	// Judges are the judge files the rule's checks declare (cleaned, relative to Dir).
	Judges []string
	// Requires are the contexts the rule `require`s (bare names).
	Requires []string
	// Invalid holds why the rule does not load, when it does not.
	Invalid []string
}

// FQN is the rule's qualified name: `<nature>/<name>`, with the plugin in front
// for a shipped rule.
func (r Rule) FQN() string {
	s := string(r.Nature) + "/" + r.Name
	if r.Plugin != "" {
		return r.Plugin + "/" + s
	}
	return s
}

// Matches reports whether a selector names the rule: its fully-qualified name,
// `<nature>/<name>`, or the bare name.
func (r Rule) Matches(sel string) bool {
	return sel == r.FQN() || sel == string(r.Nature)+"/"+r.Name || sel == r.Name
}

// LoadRules loads every rule of a `.sloprail` tree through the engine's own loader
// (the one `sr-file declarations` and every hook use), so a rule that does not load
// is reported with the loader's reasons rather than skipped. plugin names the tree
// as a plugin's, validated by the plugin rules.
func LoadRules(dotDir, plugin string) ([]Rule, error) {
	reg, err := modules.Registry()
	if err != nil {
		return nil, err
	}
	store := declaration.New(dotDir)
	if plugin != "" {
		store = declaration.NewPlugin(declaration.Origin{Plugin: plugin, Root: filepath.Dir(dotDir)})
	}
	loaded, err := store.Load(reg)
	if err != nil {
		return nil, fmt.Errorf("the declarations under %s could not be read: %w", dotDir, err)
	}
	var out []Rule
	for _, g := range loaded.FileGuards {
		out = append(out, ruleOf(declaration.NatureFileGuard, g.Name, g.Dir, dotDir, plugin, g.Checks, g.Require))
	}
	for _, g := range loaded.Gates {
		out = append(out, ruleOf(declaration.NatureGate, g.Name, g.Dir, dotDir, plugin, g.Checks, g.Require))
	}
	for _, c := range loaded.Contexts {
		out = append(out, ruleOf(declaration.NatureContext, c.Name, c.Dir, dotDir, plugin, nil, c.Require))
	}
	for _, iv := range loaded.Invalid {
		if iv.Name == "" || iv.Nature == declaration.NatureStructure {
			continue
		}
		out = append(out, Rule{
			Nature: iv.Nature, Name: iv.Name, Dir: filepath.Dir(iv.Path), Root: dotDir, Plugin: plugin,
			Invalid: iv.Reasons,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FQN() < out[j].FQN() })
	return out, nil
}

func ruleOf(n declaration.Nature, name, dir, root, plugin string, checks []declaration.Check, req []declaration.Prerequisite) Rule {
	r := Rule{Nature: n, Name: name, Dir: dir, Root: root, Plugin: plugin}
	seen := map[string]bool{}
	for _, c := range checks {
		if c.Judge == "" {
			continue
		}
		j := filepath.ToSlash(filepath.Clean(c.Judge))
		if !seen[j] {
			seen[j] = true
			r.Judges = append(r.Judges, j)
		}
	}
	for _, p := range req {
		if p.Context != "" {
			r.Requires = append(r.Requires, p.Context)
		}
	}
	return r
}

// Select keeps the rules the selectors name (all when none are given). A selector
// that names nothing is an error: a typo must not read as "nothing to test".
func Select(rules []Rule, selectors []string) ([]Rule, error) {
	if len(selectors) == 0 {
		return rules, nil
	}
	var out []Rule
	for _, sel := range selectors {
		sel = strings.Trim(sel, "/")
		var hit []Rule
		for _, r := range rules {
			if r.Matches(sel) {
				hit = append(hit, r)
			}
		}
		if len(hit) == 0 {
			names := make([]string, len(rules))
			for i, r := range rules {
				names[i] = r.FQN()
			}
			return nil, fmt.Errorf("no rule matches %q; the rules here are: %s", sel, strings.Join(names, ", "))
		}
		out = append(out, hit...)
	}
	return out, nil
}

// find resolves a `with:` entry (`context/tag-declared` or a bare name) among rules.
func find(rules []Rule, sel string) (Rule, error) {
	var hit []Rule
	for _, r := range rules {
		if r.Matches(sel) {
			hit = append(hit, r)
		}
	}
	switch len(hit) {
	case 1:
		return hit[0], nil
	case 0:
		return Rule{}, fmt.Errorf("`with:` names %q, which is no rule of this tree", sel)
	}
	return Rule{}, fmt.Errorf("`with:` names %q, which is ambiguous (%s…): write `<nature>/<name>`", sel, hit[0].FQN())
}
