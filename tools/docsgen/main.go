// Command docsgen extracts the guardrail declaration types from the source and
// emits them as JSON for the docs site to render. The doc reference for the YAML
// shapes is therefore generated from the code — the same struct tags and doc
// comments the engine compiles against — so it cannot drift from what runs.
//
// Usage:
//
//	go run ./tools/docsgen > docs/reference/generated/natures.json
//
// It reads internal/declaration/declaration.go, walks the named struct types,
// and for each field records its yaml key, Go type, and doc comment. Fields
// tagged `yaml:"-"` (loader-filled, not user YAML) are skipped.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"regexp"
	"strings"
)

var regexpMustCompile = regexp.MustCompile

// the declaration types to document, in the order they should appear.
var wanted = []string{
	"FileGuard", "Gate", "Context", "StructureGate",
	"Check", "Prerequisite", "GateTrigger", "ContextTrigger", "StructureEntry",
}

type field struct {
	YAML string `json:"yaml"`
	Type string `json:"type"`
	Doc  string `json:"doc"`
}

type typeDoc struct {
	Name   string  `json:"name"`
	Doc    string  `json:"doc"`
	Fields []field `json:"fields"`
}

func main() {
	const src = "internal/declaration/declaration.go"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, nil, parser.ParseComments)
	if err != nil {
		fmt.Fprintln(os.Stderr, "docsgen:", err)
		os.Exit(1)
	}

	found := map[string]typeDoc{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			name := ts.Name.Name
			if !contains(wanted, name) {
				continue
			}
			td := typeDoc{Name: name, Doc: clean(docText(gd.Doc))}
			for _, fld := range st.Fields.List {
				yamlKey := yamlKeyOf(fld)
				if yamlKey == "" || yamlKey == "-" {
					continue // loader-filled or untagged; not user YAML
				}
				td.Fields = append(td.Fields, field{
					YAML: yamlKey,
					Type: typeString(fld.Type),
					Doc:  clean(docText(fld.Doc)),
				})
			}
			found[name] = td
		}
	}

	// emit in the wanted order
	out := make([]typeDoc, 0, len(wanted))
	for _, name := range wanted {
		if td, ok := found[name]; ok {
			out = append(out, td)
		}
	}
	if len(out) == 0 {
		fmt.Fprintln(os.Stderr, "docsgen: no declaration types found — did the file move?")
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, "docsgen:", err)
		os.Exit(1)
	}
}

// yamlKeyOf reads the yaml tag key from a struct field, "" if none.
func yamlKeyOf(fld *ast.Field) string {
	if fld.Tag == nil {
		return ""
	}
	// tag literal includes the surrounding backticks
	tag := reflect.StructTag(strings.Trim(fld.Tag.Value, "`"))
	v, ok := tag.Lookup("yaml")
	if !ok {
		return ""
	}
	return strings.Split(v, ",")[0]
}

func docText(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}
	return cg.Text()
}

// clean collapses a Go doc comment into a single tidy sentence-ish string and
// strips internal source references the reader has no use for — parenthetical
// pointers at the spec (`main.tsp`, `.go`) and bare `GoTypeDeclaration` mentions
// of implementation types.
func clean(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Join(strings.Fields(s), " ")
	s = internalRef.ReplaceAllString(s, "")
	s = strings.Join(strings.Fields(s), " ") // re-collapse any doubled spaces
	// tidy a space left before punctuation by the removal
	s = strings.NewReplacer(" .", ".", " ,", ",", " )", ")", "( ", "(").Replace(s)
	return s
}

// internalRef matches "(... main.tsp Something)" / "(... .go ...)" — spec/source
// pointers that belong in the code, not the docs.
var internalRef = regexpMustCompile(`\s*\((?:[^()]*\b(?:\.tsp|\.go)\b[^()]*)\)`)

func typeString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.ArrayType:
		return "[]" + typeString(t.Elt)
	case *ast.StarExpr:
		return "*" + typeString(t.X)
	case *ast.SelectorExpr:
		return typeString(t.X) + "." + t.Sel.Name
	case *ast.MapType:
		return "map[" + typeString(t.Key) + "]" + typeString(t.Value)
	default:
		return fmt.Sprintf("%T", e)
	}
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
