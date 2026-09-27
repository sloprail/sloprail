package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// `sr-file field` reads one top-level field of a document with a PLAIN YAML
// reader: no schema, and no conversion of the document to JSON on the way. A
// hook asking "what does this document say its status is?" must get an answer
// for any document that is valid YAML, including YAML a schema or a JSON view
// rejects — an integer or null key, a complex key, a custom tag, `.inf` — or it
// is left to guess, and every guess is a way to be wrong about the one field it
// asked for. Which bytes are the document is decided exactly as `validate`
// decides it (document.go), so the two can never disagree about where the
// frontmatter is.

// Exit statuses. Three answers, not two, because "this file has no frontmatter"
// and "this file's frontmatter cannot be read" mean opposite things to a caller
// deciding whether a document claims something: the first claims nothing, the
// second might claim anything.
const (
	fieldExitUnreadable    = 1
	fieldExitNoFrontmatter = 2
	fieldExitUnterminated  = 3
)

// exitCodeError carries the exit status a command wants, for main to use.
type exitCodeError struct {
	code int
	err  error
}

func (e *exitCodeError) Error() string { return e.err.Error() }
func (e *exitCodeError) Unwrap() error { return e.err }

// maxAliasDepth bounds how far an alias or merge key is followed, so a document
// cannot send the lookup round a cycle.
const maxAliasDepth = 32

func newFieldCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "field <path> <key>",
		Short: "Print one top-level field of a document, read as plain YAML",
		Long: "Print one top-level field of a document, read as plain YAML — no schema.\n\n" +
			"WHICH BYTES ARE THE DOCUMENT is decided as `validate` decides it: the frontmatter of a\n" +
			".md, the whole of a .yaml, .yml or .json; '-' reads stdin, with --as.\n\n" +
			"The value is printed when it is a scalar (aliases and merge keys followed); an absent\n" +
			"field, or one whose value is a list or a map, prints nothing. Exit status:\n" +
			"  0  the document was read (the value, or nothing, is on stdout)\n" +
			"  1  the document cannot be read: it does not parse as YAML, holds more than one YAML\n" +
			"     document, or defines the field (or a merge key) twice; or the input is not a\n" +
			"     regular file, or is larger than the cap\n" +
			"  2  the file has no frontmatter: no opening --- fence\n" +
			"  3  an opening --- fence is never closed: a forgotten close, or a rule above prose\n" +
			"     (the caller reads the text after it to tell which)\n" +
			"  64 a usage error: an unknown command, flag or argument count\n\n" +
			"EXAMPLES:\n" +
			"  sr-file field memories/topics/a/units/01/UNIT.md status\n" +
			"  jq -r .event.newContent payload.json | sr-file field - status --as .md",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          runFieldCmd,
	}
	cmd.Flags().String("as", "", "How to read bytes on stdin: .md, .yaml or .json. Required with '-', and refused with a path")
	return cmd
}

func runFieldCmd(cmd *cobra.Command, args []string) error {
	as, _ := cmd.Flags().GetString("as")
	doc, err := readInput(cmd, "field", args[0], as)
	if err != nil {
		switch {
		case errors.Is(err, errNoFrontmatter):
			return &exitCodeError{fieldExitNoFrontmatter, err}
		case errors.Is(err, errUnterminatedFrontmatter):
			return &exitCodeError{fieldExitUnterminated, err}
		}
		return &exitCodeError{fieldExitUnreadable, err}
	}
	value, err := documentField(doc, args[1])
	if err != nil {
		return &exitCodeError{fieldExitUnreadable, fmt.Errorf("sr-file field: %s: %w", doc.Path, err)}
	}
	if value != nil {
		fmt.Fprintln(cmd.OutOrStdout(), *value)
	}
	return nil
}

// documentField is the scalar value of the top-level key in doc, or nil when the
// key is absent or its value is not a scalar. An error means the document cannot
// be read: it does not parse, or it defines the key more than once (a reader
// that took either would be guessing).
func documentField(doc Document, key string) (*string, error) {
	var root yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(doc.Data))
	if err := dec.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil // an empty document holds no field
		}
		return nil, fmt.Errorf("the document does not parse as YAML: %w", err)
	}
	// One document only: a reader that stops at the first and one that reads
	// the last would give two answers (`...` then a second status).
	var next yaml.Node
	if err := dec.Decode(&next); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("the frontmatter holds more than one YAML document, so which one's %q counts is not decidable", key)
	}
	node := &root
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return nil, nil
		}
		node = node.Content[0]
	}
	found, err := lookupKey(node, key, 0, map[*yaml.Node]bool{})
	if err != nil || found == nil || found.Kind != yaml.ScalarNode {
		return nil, err
	}
	if found.Tag == "!!null" {
		return nil, nil
	}
	return &found.Value, nil
}

// lookupKey finds key among a mapping's own pairs, then in anything it merges
// (`<<:`), and returns its value with aliases followed. visited holds every
// mapping already searched in this lookup: a merge chain that names the same
// mapping many times (`<<: [*a, *a, …]` at every level) searches it once, so
// the lookup is linear in the document, never exponential in its aliases.
func lookupKey(m *yaml.Node, key string, depth int, visited map[*yaml.Node]bool) (*yaml.Node, error) {
	if depth > maxAliasDepth {
		return nil, fmt.Errorf("aliases or merge keys nest deeper than %d", maxAliasDepth)
	}
	m = deref(m)
	if m == nil || m.Kind != yaml.MappingNode || visited[m] {
		return nil, nil
	}
	visited[m] = true
	var found, merge *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := deref(m.Content[i]), m.Content[i+1]
		if k == nil || k.Kind != yaml.ScalarNode {
			continue
		}
		if k.Tag == "!!merge" {
			// Two merge keys in one mapping are a key defined twice, the same as
			// two `status:` keys: readers disagree about which one wins.
			if merge != nil {
				return nil, fmt.Errorf("the merge key << is defined more than once")
			}
			merge = v
			continue
		}
		if k.Value != key {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("%q is defined more than once", key)
		}
		found = deref(v)
	}
	if found != nil || merge == nil {
		return found, nil
	}
	merge = deref(merge)
	sources := []*yaml.Node{merge}
	if merge != nil && merge.Kind == yaml.SequenceNode {
		sources = merge.Content
	}
	for _, src := range sources {
		if v, err := lookupKey(src, key, depth+1, visited); err != nil || v != nil {
			return v, err
		}
	}
	return nil, nil
}

// deref follows an alias to the node it names.
func deref(n *yaml.Node) *yaml.Node {
	for i := 0; n != nil && n.Kind == yaml.AliasNode && i < maxAliasDepth; i++ {
		n = n.Alias
	}
	return n
}
