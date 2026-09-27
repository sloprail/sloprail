package main

import (
	"bytes"
	"errors"
)

// fence is the `---` line that opens and closes a Markdown document's leading
// YAML.
var fence = []byte("---")

// isFence reports whether a line is a frontmatter fence: exactly `---` once
// whitespace is trimmed.
//
// One predicate, used for both the opening and the closing fence. They were
// the same shape, so telling them apart would be inventing a distinction the
// format does not have.
//
// Exact rather than prefix, because a prefix match cannot tell a fence from a
// line that starts like one. `---yaml` is a person reaching for the fenced-code
// spelling of frontmatter, and `----` is a typo or a horizontal rule; reading
// either as a fence means parsing the file as something its author did not
// write. Refusing is what puts the mistake in front of them.
func isFence(line []byte) bool {
	return bytes.Equal(bytes.TrimSpace(line), fence)
}

// splitFrontmatter separates the leading YAML document from the prose beneath
// it. The prose is returned untouched: it is documentation and rubric at once,
// and normalising it would change what a judge is judging against.
//
// The leading YAML is returned with its lines joined as they appeared, so a
// caller that reports positions inside it counts from the fence, not from the
// top of the file — line N of `front` is line N+1 of the file.
//
// This once lived in internal/guardrail, exported so `sr-file` could split a
// `.md` the same way a GUARDRAIL.md was split — one implementation, one answer
// to "where does the frontmatter end". The old format is gone and `sr-file` is
// now the only caller, so it lives here, unexported, beside the ExtractDocument
// that uses it.
func splitFrontmatter(data []byte) (front, body []byte, err error) {
	lines := bytes.SplitAfter(data, []byte("\n"))
	if len(lines) == 0 || !isFence(lines[0]) {
		return nil, nil, noFrontmatterError("no frontmatter: a document begins with a --- fence")
	}

	for i := 1; i < len(lines); i++ {
		if isFence(lines[i]) {
			front = bytes.Join(lines[1:i], nil)
			body = bytes.Join(lines[i+1:], nil)
			return front, body, nil
		}
	}
	return nil, nil, noFrontmatterError("unterminated frontmatter: no closing --- fence")
}

// errNoFrontmatter is what both split failures are: the file carries no
// frontmatter document — no opening fence, or an opening `---` with no closing
// one, which is a horizontal rule at the top of prose rather than a fence. Test
// with errors.Is; the messages stay the split's own.
var errNoFrontmatter = errors.New("no frontmatter")

// noFrontmatterError is a split failure that is errNoFrontmatter.
type noFrontmatterError string

func (e noFrontmatterError) Error() string        { return string(e) }
func (e noFrontmatterError) Is(target error) bool { return target == errNoFrontmatter }
