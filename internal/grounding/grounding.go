// Package grounding is the one place a command line's CITATIONS are read.
//
// A grounded change carries its citation on the COMMAND that makes it, never
// inside the content it writes: repo content stays self-sufficient (derived
// text only), and the source the change was grounded in travels on the action,
// where a guardrail can verify it. Two commands carry citations:
//
//	sr-file write|edit|delete <path> --cite:<source-types> <quote> ...
//	sr-session trajectory cite [--source-types <types>] <quote>
//
// The first performs a file change and names what grounds it; the second
// grounds whatever command it is chained with (`cite '<quote>' && git push`).
//
// Three readers share this grammar and must never disagree about it: the
// sr-file binary parsing its own arguments, commandmod predicting which file an
// sr-file invocation touches, and sr-session attaching the resolved citations
// to the events a tool call produces. So the grammar lives here, once.
//
// This package parses and converts; it never reads a transcript or the tree.
// Resolving a request into a Citation is transcript.ResolveCitation's.
package grounding

import (
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/transcript"
)

// FieldCitations is the event field that carries the citations an action was
// grounded in. Declared on PreCommandInvoke and on every file kind; always a
// list, empty when the action named none.
const FieldCitations = "citations"

// Keys within one entry of FieldCitations.
const (
	KeyQuote       = "quote"
	KeySourceTypes = "sourceTypes"
	KeyPath        = "path"
	KeyLine        = "line"
)

// CitationsDecl is FieldCitations' declaration, shared by every module that
// carries it so a matcher reads the same element shape on every kind. The
// element is enumerated so a typo inside a predicate (`any(citations, .qoute
// == "x")`) is refused at load rather than evaluating false forever.
func CitationsDecl() module.FieldDecl {
	return module.FieldDecl{
		Name: FieldCitations,
		Type: module.TypeList,
		Elem: &module.FieldDecl{
			Type: module.TypeMap,
			Fields: []module.FieldDecl{
				{Name: KeyQuote, Type: module.TypeString},
				{Name: KeySourceTypes, Type: module.TypeList, Elem: &module.FieldDecl{Type: module.TypeString}},
				{Name: KeyPath, Type: module.TypeString},
				{Name: KeyLine, Type: module.TypeInt},
			},
		},
	}
}

// ToWire flattens citations to the plain containers a matcher and a hook's JSON
// decoder read. Never nil: an action with no citation carries an empty list, so
// `len(citations) == 0` holds rather than erroring.
func ToWire(cs []transcript.Citation) []any {
	out := make([]any, 0, len(cs))
	for _, c := range cs {
		types := make([]any, len(c.SourceTypes))
		for i, s := range c.SourceTypes {
			types[i] = string(s)
		}
		out = append(out, map[string]any{
			KeyQuote:       c.Quote,
			KeySourceTypes: types,
			KeyPath:        c.Path,
			KeyLine:        c.Line,
		})
	}
	return out
}

// FromWire reads citations back off an event field. Entries that are not
// citation-shaped are skipped: nothing here invents a citation the field did
// not carry.
func FromWire(v any) []transcript.Citation {
	list, _ := v.([]any)
	var out []transcript.Citation
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		c := transcript.Citation{}
		c.Quote, _ = m[KeyQuote].(string)
		c.Path, _ = m[KeyPath].(string)
		switch n := m[KeyLine].(type) {
		case int:
			c.Line = n
		case float64:
			c.Line = int(n)
		}
		switch types := m[KeySourceTypes].(type) {
		case []any:
			for _, t := range types {
				if s, ok := t.(string); ok {
					c.SourceTypes = append(c.SourceTypes, transcript.SourceType(s))
				}
			}
		case []string:
			for _, s := range types {
				c.SourceTypes = append(c.SourceTypes, transcript.SourceType(s))
			}
		}
		if c.Quote == "" || c.Path == "" || c.Line == 0 {
			continue
		}
		out = append(out, c)
	}
	return out
}
