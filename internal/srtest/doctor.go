package srtest

import (
	"encoding/json"
	"sort"
)

// Uncovered returns the rules ("<nature>:<rule>") listed by any case that no case's events
// show deciding (or, for a context, activating). A structure gate is "structure:<plugin>/structure"
// (or "structure:structure" for the project's) and is covered by a StructureChecked event of that rule.
func Uncovered(results []Result) []string {
	seen := map[string]bool{}
	for _, r := range results {
		for _, raw := range r.Metadata.Events {
			var e struct{ Kind, Rule string }
			if json.Unmarshal(raw, &e) != nil {
				continue
			}
			switch e.Kind {
			case "GateChecked":
				seen["gate:"+e.Rule] = true
			case "FileGuardChecked":
				seen["file-guard:"+e.Rule] = true
			case "ContextActivated":
				seen["context:"+e.Rule] = true
			case "StructureChecked":
				seen["structure:"+e.Rule] = true
			}
		}
	}
	set := map[string]bool{}
	for _, r := range results {
		for _, rule := range r.Metadata.Rules {
			if seen[rule] {
				continue
			}
			set[rule] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
