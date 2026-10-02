// Package splitrequire moves the transcript-dependent `require:` entries (skill,
// context) off file-guards and onto gates, over a tree of `.sloprail` roots.
//
// A file-guard judges committed bytes at Stop and cannot see the session; the
// loader refuses such an entry (declaration.ErrRetiredKey). For each offending
// file-guard this writes the replacement gate `<name>-requires` (or, when the
// sibling gate `<name>` already holds the same requirement, reuses it), removes
// the entries from the file-guard, and deletes a file-guard left with neither a
// require nor a check. It is idempotent: a converted tree has nothing to do.
package splitrequire

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sloprail/sloprail/internal/declaration"
)

// Action is one planned change.
type Action struct {
	Guard   string // path of the file-guard.yaml
	Gate    string // gate folder name that holds the requirement
	NewGate string // path of a gate.yaml to write ("" when an existing gate covers it)
	Content string // the new gate's content
	Delete  bool   // the file-guard is left empty and its folder is removed
	Err     error  // the rule needs a hand conversion
}

// Run converts every file-guard under roots (directories searched recursively
// for `file-guard/<name>/file-guard.yaml`). With apply false it only plans.
func Run(roots []string, apply bool, out io.Writer) ([]Action, error) {
	var guards []string
	for _, r := range roots {
		err := filepath.WalkDir(r, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if !d.IsDir() && d.Name() == "file-guard.yaml" && filepath.Base(filepath.Dir(filepath.Dir(p))) == "file-guard" {
				guards = append(guards, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(guards)
	var actions []Action
	for _, g := range guards {
		a, ok, err := plan(g)
		if err != nil {
			return actions, err
		}
		if !ok {
			continue
		}
		actions = append(actions, a)
		fmt.Fprintln(out, describe(a))
		if apply && a.Err == nil {
			if err := applyAction(a); err != nil {
				return actions, err
			}
		}
	}
	if len(actions) == 0 {
		fmt.Fprintln(out, "nothing to convert")
	}
	return actions, nil
}

func describe(a Action) string {
	switch {
	case a.Err != nil:
		return fmt.Sprintf("MANUAL  %s: %v", a.Guard, a.Err)
	case a.NewGate != "" && a.Delete:
		return fmt.Sprintf("SPLIT   %s -> new gate %s; file-guard removed (nothing left to judge)", a.Guard, a.NewGate)
	case a.NewGate != "":
		return fmt.Sprintf("SPLIT   %s -> new gate %s; entries removed from the file-guard", a.Guard, a.NewGate)
	case a.Delete:
		return fmt.Sprintf("COVERED %s: gate %q already holds the requirement; file-guard removed (nothing left to judge)", a.Guard, a.Gate)
	default:
		return fmt.Sprintf("COVERED %s: gate %q already holds the requirement; entries removed from the file-guard", a.Guard, a.Gate)
	}
}

func plan(guardPath string) (Action, bool, error) {
	data, err := os.ReadFile(guardPath)
	if err != nil {
		return Action{}, false, err
	}
	var fg declaration.FileGuard
	if err := yaml.Unmarshal(data, &fg); err != nil {
		return Action{}, false, fmt.Errorf("%s: %w", guardPath, err)
	}
	move, keep := declaration.TranscriptRequires(fg.Require)
	if len(move) == 0 {
		return Action{}, false, nil
	}
	dir := filepath.Dir(guardPath)
	name := filepath.Base(dir)
	root := filepath.Dir(filepath.Dir(dir))
	a := Action{Guard: guardPath, Delete: len(keep) == 0 && len(fg.Checks) == 0}

	for _, gname := range []string{name, declaration.ReplacementGateName(name)} {
		if gateCovers(filepath.Join(root, "gate", gname, "gate.yaml"), move) {
			a.Gate = gname
			return a, true, nil
		}
	}
	gname := declaration.ReplacementGateName(name)
	a.Gate = gname
	content, err := declaration.ReplacementGateYAML(name, fg.Match, move, "../../file-guard/"+name+"/")
	if err != nil {
		a.Err = err
		return a, true, nil
	}
	a.NewGate = filepath.Join(root, "gate", gname, "gate.yaml")
	a.Content = content
	if _, err := os.Stat(a.NewGate); err == nil {
		a.Err = fmt.Errorf("gate %q exists but does not hold the same requirement; merge by hand", gname)
	}
	return a, true, nil
}

func gateCovers(path string, move []declaration.Prerequisite) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var g declaration.Gate
	if yaml.Unmarshal(data, &g) != nil {
		return false
	}
	for _, m := range move {
		found := false
		for _, r := range g.Require {
			if r.Skill == m.Skill && r.Context == m.Context && strings.Join(r.Files, ",") == strings.Join(m.Files, ",") {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func applyAction(a Action) error {
	if a.NewGate != "" {
		if err := os.MkdirAll(filepath.Dir(a.NewGate), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(a.NewGate, []byte(a.Content), 0o644); err != nil {
			return err
		}
	}
	if a.Delete {
		return os.RemoveAll(filepath.Dir(a.Guard))
	}
	data, err := os.ReadFile(a.Guard)
	if err != nil {
		return err
	}
	rewritten, err := stripRequire(string(data), a.Gate)
	if err != nil {
		return fmt.Errorf("%s: %w", a.Guard, err)
	}
	return os.WriteFile(a.Guard, []byte(rewritten), 0o644)
}

// stripRequire rewrites the top-level `require:` block of a file-guard, keeping
// the entries that are not transcript-dependent (raw text, comments intact) and
// leaving a comment that points at the gate.
func stripRequire(src, gate string) (string, error) {
	lines := strings.Split(src, "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "require:") {
			start = i
			break
		}
	}
	if start < 0 {
		return "", fmt.Errorf("no top-level require: block")
	}
	end := start + 1
	for end < len(lines) {
		l := lines[end]
		if l != "" && l[0] != ' ' && l[0] != '\t' && l[0] != '-' {
			break
		}
		end++
	}
	// trailing blank lines belong after the block
	for end > start+1 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	var entries [][]string
	for _, l := range lines[start+1 : end] {
		trim := strings.TrimLeft(l, " ")
		if strings.HasPrefix(trim, "- ") || trim == "-" {
			entries = append(entries, []string{l})
		} else if len(entries) > 0 {
			entries[len(entries)-1] = append(entries[len(entries)-1], l)
		}
	}
	var kept []string
	for _, e := range entries {
		var ps []declaration.Prerequisite
		if err := yaml.Unmarshal([]byte(strings.Join(e, "\n")), &ps); err != nil {
			return "", err
		}
		if len(ps) == 1 && !ps[0].TranscriptDependent() {
			kept = append(kept, e...)
		}
	}
	note := fmt.Sprintf("# The skill/context requirement of this rule lives on its gate: ../../gate/%s/gate.yaml\n# (a file-guard cannot see the session; `require: citation` stays here, read from the commits).", gate)
	var repl []string
	repl = append(repl, strings.Split(note, "\n")...)
	if len(kept) > 0 {
		repl = append(repl, "require:")
		repl = append(repl, kept...)
	}
	out := append(append(append([]string{}, lines[:start]...), repl...), lines[end:]...)
	return strings.Join(out, "\n"), nil
}
