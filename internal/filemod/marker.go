package filemod

import (
	"regexp"
	"strings"
)

// Marker is one `sr:<kind> <fqn>` annotation found in a file's text.
//
// A marker is what lets a rule bind to a place in the code rather than to a
// path. A path says where a file sits today; a marker says what a piece of code
// IS, and survives the file being split, renamed, or moved — which is what makes
// a rule about it hold across a refactor that a path-bound rule would quietly
// stop applying to.
type Marker struct {
	// Kind is the word after `sr:`.
	//
	// Not drawn from a fixed set. A project marks what it needs to mark, and
	// the kinds worth having are the ones its own rules ask about, so an engine
	// that shipped the list would be an engine that had guessed. This scanner
	// therefore reads EVERY `sr:` marker, not only the kinds some loaded
	// guardrail happens to bind to — see Scan.
	Kind string

	// FQN is what the marker names: whatever identifies the thing in the
	// project's own terms — a dotted name, a path, a URL.
	//
	// The spec constrains it to what a URL admits, which rules out the
	// whitespace that would make the written form ambiguous. This reader does
	// not ENFORCE that, and the difference is worth stating rather than
	// glossing: it reports what the line holds between the separators, whatever
	// that is. What ends the fqn is one `\s` as RE2 defines it — [\t\n\f\r ] —
	// so a vertical tab, a NUL, or a non-breaking space does NOT end it and
	// lands inside the fqn instead. See
	// TestScan_SeparatorIsGoRegexpWhitespaceAndNothingElse.
	//
	// Reporting rather than rejecting is the deliberate half. A scanner that
	// dropped a marker whose fqn it disapproved of would be a marker silently
	// missing, which reads as unmarked code — the failure this engine exists to
	// prevent. A surprising fqn is visible; an absent one is not.
	FQN string

	// Line is the 1-based line the marker sits on.
	//
	// Where the marker IS, not the extent of what it describes. That extent is
	// a language question — where a function ends, where a block closes — and
	// an engine that answered it would be answering it wrong for every language
	// it had not been told about. A rule that needs the extent reads the file,
	// which it can do, knowing things the engine does not.
	//
	// A Go `int`, though the spec's model says int32. The width here is set by
	// what the type checker will hold the matcher to: module.TypeInt maps to
	// expr's types.Int, which is TypeOf(0). Putting an int32 in the event would
	// let `.line > 10` type-check against a type the vm is not holding. The
	// spec's int32 is the wire contract; this is the in-process representation
	// of it, and every line number a file can have fits in both.
	Line int
}

// markerPattern is the reader, taken verbatim from the writer's own
// (services/mark/marker.go on impl/sr-mark), so that what sr-mark writes is
// exactly what this reads back. The written form is:
//
//	<leader> sr:<kind> <fqn>
//
// with `//`, `#` or `--` as the leader. The whole line must be the marker: the
// anchors are what keep this from reading a `sr:` that happens to sit at the end
// of a line of code, where the text before it decides what it means and this
// scanner cannot see that text.
//
// Group 1 is the kind, group 2 the fqn. The writer's pattern hard-codes one
// kind because it is deleting a kind's markers; this one captures the kind,
// because it is reading whatever a file carries.
var markerPattern = regexp.MustCompile(`^\s*(?://|#|--)\s*sr:(\S+)\s+(\S+)\s*$`)

// Scan reads every `sr:` marker out of a file's text, in the order the lines
// carry them.
//
// Every marker, not only the kinds some guardrail asked about. A field that
// meant "the markers you bound to" would make `len(markers) == 0` — the natural
// way to write an unmarked-code rule — mean something different depending on
// what other guardrails the project happened to declare, so a rule would start
// or stop firing because of an edit to a file it has nothing to do with. That is
// the "looks satisfied" failure this engine exists to prevent.
//
// Returns an empty, non-nil slice when the text carries no markers. The
// distinction is observable: `markers == nil` and `len(markers) == 0` are
// different tests in expr, and a rule written against emptiness should not have
// to know which one it will be handed.
//
// # What is NOT excluded
//
// A marker inside a string literal, a heredoc, or a fenced code block IS
// returned. Deciding otherwise means knowing where a literal starts and ends,
// which is the same per-language question Marker.Line refuses to answer: the
// rules differ for raw strings, for triple quotes, for nested fences, and a
// scanner that guessed would be wrong for every language it had not been told
// about. Guessing in the other direction is the worse failure of the two — a
// marker dropped is a rule that silently does not fire on code that IS marked,
// and that reads as the rule being satisfied. An extra marker read out of a
// documentation example is visible: the rule fires, and someone looks.
func Scan(text string) []Marker {
	markers := []Marker{}
	if text == "" {
		return markers
	}

	// SplitSeq over "\n" rather than a bufio.Scanner: a Scanner would drop the
	// distinction between a file ending in a newline and one that does not, and
	// a marker on the last line of a file with no trailing newline is an
	// ordinary thing to write. Splitting keeps every line, and the trailing
	// empty string a final newline produces matches nothing.
	//
	// CRLF needs nothing here. Splitting on "\n" leaves the "\r" at the end of
	// each line, and the pattern's trailing `\s*$` absorbs it — `\r` IS `\s` to
	// RE2, and `\S+` cannot swallow it for the same reason. An explicit
	// TrimSuffix stood here first; deleting it changed no test, which is what
	// established that the pattern is doing the work rather than the trim. Note
	// this holds only for a TRAILING carriage return: a `\r` in the middle of a
	// line ends the fqn and leaves a second token, and the `$` anchor then
	// rejects the line. See TestScan_MidLineCRIsNotStripped.
	line := 0
	for raw := range strings.SplitSeq(text, "\n") {
		line++
		m := markerPattern.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		markers = append(markers, Marker{Kind: m[1], FQN: m[2], Line: line})
	}
	return markers
}

// fields is the wire form of one marker: the map an event carries and a matcher
// reads inside `any(markers, .kind == "docs")`.
//
// Line is a Go `int` because that is what the declaration says
// (module.TypeInt maps to expr's types.Int, which is TypeOf(0)). A different
// width here would type-check against something the vm is not holding.
func (m Marker) fields() map[string]any {
	return map[string]any{
		KeyMarkerKind: m.Kind,
		KeyMarkerFQN:  m.FQN,
		KeyMarkerLine: m.Line,
	}
}

// markerFields converts a scan result to what an event carries. Non-nil for an
// empty scan, for the reason Scan documents.
func markerFields(markers []Marker) []any {
	out := make([]any, 0, len(markers))
	for _, m := range markers {
		out = append(out, m.fields())
	}
	return out
}
