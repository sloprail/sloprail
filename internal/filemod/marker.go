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
	// project's own terms — a dotted name, a path, a URL, or a quoted phrase.
	//
	// # Two written forms
	//
	// A BARE fqn is a single run of non-whitespace — `pkg.Thing`, a path, a
	// URL. This is the original form and the common one.
	//
	// A QUOTED fqn is a `"..."`-delimited string whose inner text — spaces and
	// all — is the fqn, with the surrounding quotes stripped. It exists because
	// a marker's name is not always a token: `# sr:asked "keep the original
	// transcript_path, just add the new section"` records a phrase a person
	// actually said, which a later check grounds against the trajectory. A bare
	// `(\S+)` truncates that at the first space; the quoted form carries it
	// whole. See sloprail-community's examples/no-unasked-deletion and TestScan_QuotedFQN*.
	//
	// The quoted form's grammar is deliberately small, so what the writer emits
	// and what this reads back cannot drift on a corner:
	//   - The inner text CANNOT contain a `"`. There is no escaping and no
	//     nesting — `"he said \"hi\""` is not a marker, it is a malformed line
	//     the anchors reject. A name that needs an embedded quote is out of
	//     scope, and rejecting it is visible where a half-parsed one would not
	//     be.
	//   - A line whose fqn STARTS with `"` is committed to the quoted form: it
	//     must close and be followed only by whitespace, or it is not a marker
	//     at all. An unterminated `"quote` does not silently fall back to a bare
	//     fqn that begins with a quote character — that would be a surprising
	//     name read out of a typo. A `"` anywhere OTHER than the first character
	//     of a bare token is an ordinary character and stays in the fqn.
	//   - `""` is a marker whose fqn is the empty string. Degenerate, but read
	//     rather than rejected for the same reason every other odd fqn is: a
	//     check that receives it (an empty quote grounds to nothing) refuses on
	//     its own terms; a marker silently dropped reads as unmarked code.
	//
	// The spec constrains a bare fqn to what a URL admits, which rules out the
	// whitespace that would make that form ambiguous — the quoted form is how a
	// name WITH whitespace is written unambiguously. This reader does not
	// ENFORCE the URL-safety of a bare fqn, and the difference is worth stating
	// rather than glossing: it reports what the line holds between the
	// separators, whatever that is. What ends a bare fqn is one `\s` as RE2
	// defines it — [\t\n\f\r ] — so a vertical tab, a NUL, or a non-breaking
	// space does NOT end it and lands inside the fqn instead. See
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

// markerPattern is the reader. The written forms are:
//
//	<leader> sr:<kind> <bare-fqn>
//	<leader> sr:<kind> "<quoted fqn, spaces and all>"
//
// with `//`, `#` or `--` as the leader. The whole line must be the marker: the
// anchors are what keep this from reading a `sr:` that happens to sit at the end
// of a line of code, where the text before it decides what it means and this
// scanner cannot see that text.
//
// Group 1 is the kind. The fqn is EITHER group 2 (the inner text of a `"..."`,
// captured without the quotes) OR group 3 (a bare non-whitespace token). The
// two branches are mutually exclusive; Scan reads whichever participated, told
// apart by submatch index rather than by an empty string, so that `""` — a
// legitimately empty quoted fqn — is not confused with a branch that did not
// match. See Marker.FQN for the grammar and why it is this small.
//
// The bare branch is `[^\s"]\S*`, not `\S+`: its first character may not be a
// quote, which is what commits a line whose fqn starts with `"` to the quoted
// form. A `"` in any later position is an ordinary character and stays in the
// bare fqn, preserving the original reader's behavior for a name that happens
// to contain one.
//
// The kind stays `(\S+)`: a kind is a single word by construction (it is the
// token after `sr:` a rule binds to), and nothing has asked for a quoted kind.
var markerPattern = regexp.MustCompile(`^\s*(?://|#|--)\s*sr:(\S+)\s+(?:"([^"]*)"|([^\s"]\S*))\s*$`)

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
//
// A marker inside a markdown file's YAML frontmatter — a `# sr:...` line
// between the `---` fences at the top of the file — IS returned, and by the
// same mechanism as everything else: the `#` leader is one this reader already
// matches, and the scan is over raw lines, so the `---` fences need no special
// handling. This is deliberate and load-bearing. A YAML comment parses to
// nothing — comment-only frontmatter is a valid, EMPTY YAML document — so a
// marker written as `# sr:asked "<quote>"` is invisible to any YAML parser and
// must be read out of the text, which is exactly what this does. It is how a
// markdown file carries a marker without having to invent a real frontmatter
// field to hang it on. See sloprail-community's examples/no-unasked-deletion and
// TestScan_MarkerInMarkdownFrontmatter.
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
		// Index form, not FindStringSubmatch: the fqn is one of two alternation
		// branches, and an unset group and an empty-string match both read as ""
		// from the string form. The index (-1 when a group did not participate)
		// is what tells the quoted branch that matched `""` apart from the bare
		// branch — see markerPattern. Groups: 1 kind; 2 quoted inner; 3 bare.
		loc := markerPattern.FindStringSubmatchIndex(raw)
		if loc == nil {
			continue
		}
		kind := raw[loc[2]:loc[3]]
		var fqn string
		if loc[4] != -1 { // the quoted branch participated
			fqn = raw[loc[4]:loc[5]]
		} else { // the bare branch
			fqn = raw[loc[6]:loc[7]]
		}
		markers = append(markers, Marker{Kind: kind, FQN: fqn, Line: line})
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
