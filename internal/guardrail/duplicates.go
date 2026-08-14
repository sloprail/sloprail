package guardrail

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// duplicateKeys reports every repeated mapping key in a YAML document, as
// "hooks.PreFileCreate defined twice (lines 3, 7)".
//
// This catches no bug that would otherwise ship. yaml.v3 already refuses a
// duplicate mapping key when unmarshalling into a Go value, at every depth,
// including inside sequences — verified, against the belief that it silently
// kept the last one. Nothing here stands between a duplicate and a silent
// half-rule, because nothing did.
//
// What it buys is the message. The library says
//
//	yaml: unmarshal errors:
//	  line 5: mapping key "PreFileCreate" already defined at line 3
//
// which names a YAML concept and a line, and leaves an author to work out
// which of their event bindings just vanished. This says the key's path in the
// declaration's own vocabulary and both lines, and says it for every duplicate
// in the file rather than the first — the library stops at one, since an
// unmarshal error ends the document.
//
// That is a smaller claim than the one this code was written for, and worth
// keeping only for as long as the wording earns its keep. Reading yaml.Node
// rather than a map is what makes it possible at all: a map has collapsed the
// duplicate before anyone can look.
func duplicateKeys(front []byte) ([]Problem, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(front, &doc); err != nil {
		return nil, fmt.Errorf("parse frontmatter: %w", err)
	}
	var found []Problem
	walkDuplicates(&doc, "", &found)
	return found, nil
}

// walkDuplicates descends every mapping in the tree. Duplicates matter at any
// depth: two `command` keys in one hook are the same silent overwrite as two
// event kinds, one level down.
func walkDuplicates(n *yaml.Node, path string, found *[]Problem) {
	if n == nil {
		return
	}
	if n.Kind == yaml.MappingNode {
		// Content alternates key, value. First line wins the "defined at"
		// position, so the report reads in the order the file does.
		seen := make(map[string]int, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if first, dup := seen[k.Value]; dup {
				*found = append(*found, Problem{
					Kind:    ErrDuplicateKey,
					Fault:   FaultDeclaration,
					Binding: -1,
					Hook:    -1,
					Detail: fmt.Sprintf("%s defined twice (lines %d, %d) — only one of them can take effect",
						join(path, k.Value), first, k.Line),
				})
				continue
			}
			seen[k.Value] = k.Line
		}
	}
	for i, c := range n.Content {
		child := path
		if n.Kind == yaml.MappingNode {
			if i%2 == 1 {
				child = join(path, n.Content[i-1].Value)
			} else {
				continue // keys carry no nested mappings worth naming
			}
		}
		walkDuplicates(c, child, found)
	}
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
