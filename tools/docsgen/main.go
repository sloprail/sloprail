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
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
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
	outDir := flag.String("out", "", "directory to write the JSON files into (default: stdout, natures only)")
	flag.Parse()

	natures := genNatures("internal/declaration/declaration.go")
	events := genEvents("internal/declaration/events.go")

	if *outDir == "" {
		writeJSON(os.Stdout, natures)
		return
	}
	writeFile(filepath.Join(*outDir, "natures.json"), natures)
	writeFile(filepath.Join(*outDir, "events.json"), events)
	fmt.Fprintf(os.Stderr, "docsgen: wrote natures.json (%d types) and events.json (%d kinds) to %s\n",
		len(natures), len(events.Kinds), *outDir)
}

// genNatures walks the declaration struct types into typed field docs.
func genNatures(src string) []typeDoc {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, nil, parser.ParseComments)
	if err != nil {
		fatal(err)
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

	out := make([]typeDoc, 0, len(wanted))
	for _, name := range wanted {
		if td, ok := found[name]; ok {
			out = append(out, td)
		}
	}
	if len(out) == 0 {
		fatal(fmt.Errorf("no declaration types found in %s — did the file move?", src))
	}
	return out
}

type eventKind struct {
	Name string `json:"name"`
	Doc  string `json:"doc"`
}

type eventDoc struct {
	Kinds   []eventKind `json:"kinds"`
	Aliases []eventKind `json:"aliases"`
}

// genEvents reads the concrete event-kind constants and the alias constants from
// events.go — the canonical list the modules declare — so the event vocabulary
// reference comes from the same source the engine binds against.
func genEvents(src string) eventDoc {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, nil, parser.ParseComments)
	if err != nil {
		fatal(err)
	}
	var out eventDoc
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) == 0 {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok {
				continue
			}
			name := strings.Trim(lit.Value, `"`)
			ident := vs.Names[0].Name
			doc := clean(docText(vs.Doc))
			// Go doc comments start with the identifier ("KindPreToolUse fires
			// …"); strip that lead-in so the docs read as a plain sentence.
			doc = strings.TrimSpace(strings.TrimPrefix(doc, ident))
			if doc != "" {
				doc = strings.ToUpper(doc[:1]) + doc[1:]
			}
			switch {
			case strings.HasPrefix(ident, "Kind"):
				out.Kinds = append(out.Kinds, eventKind{Name: name, Doc: doc})
			case strings.HasPrefix(ident, "Alias"):
				out.Aliases = append(out.Aliases, eventKind{Name: name, Doc: doc})
			}
		}
	}
	if len(out.Kinds) == 0 {
		fatal(fmt.Errorf("no event kinds found in %s — did the file move?", src))
	}
	return out
}

func writeJSON(w *os.File, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fatal(err)
	}
}

func writeFile(path string, v any) {
	fh, err := os.Create(path)
	if err != nil {
		fatal(err)
	}
	defer fh.Close()
	writeJSON(fh, v)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "docsgen:", err)
	os.Exit(1)
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
