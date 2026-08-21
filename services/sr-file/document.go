// This file answers the one question CUE has no opinion about: which bytes of a
// file are the document.
//
// CUE vets a document against a schema. It does not know that a Markdown file is
// a YAML document wearing prose below it — hand it the whole `.md` and the error
// you get back is about the prose, which is not what the schema was written to
// check. So the extension decides: the frontmatter of a `.md`, the whole of a
// `.yaml` or `.json`.
//
// The set of extensions is CLOSED and an unknown one is an ERROR rather than a
// guess. Guessing would mean picking a splitter for a file whose shape we do not
// know, and being wrong about that silently produces an error about the wrong
// bytes — the failure mode this whole file exists to prevent.
package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Document is the bytes a schema is checked against, plus what a reader needs to
// point at a position inside them.
type Document struct {
	// Path is the file as the caller named it, carried so every message can say
	// which file — a hook that reports "field is required" without naming the
	// file leaves the agent to guess which of the files it just wrote is meant.
	Path string

	// Data is the extracted document: the frontmatter of a .md, the whole of a
	// .yaml or .json.
	Data []byte

	// LineOffset is how many lines of the file sit ABOVE Data. For a .md that is
	// 1 — the opening `---` fence — so a position CUE reports at line N of the
	// document is line N+LineOffset of the file. For a whole-file format it is 0.
	//
	// Without this, a hook's error points into a file at a line that is not the
	// line the author must edit, which is a wrong answer delivered with the same
	// confidence as a right one.
	LineOffset int

	// Format is how Data must be decoded — "yaml" or "json".
	Format string
}

// ErrUnknownExtension is returned for a file whose extension does not decide a
// document. A sentinel so a caller can branch on the class of fault rather than
// on its wording.
type ErrUnknownExtension struct {
	Path string
	Ext  string
}

func (e *ErrUnknownExtension) Error() string {
	return fmt.Sprintf("%s: unsupported file extension %q — which bytes are the document is decided by the extension, and this one decides nothing (supported: .md, .yaml, .yml, .json)", e.Path, e.Ext)
}

// ExtractDocument returns the bytes of path that a schema should be checked
// against, given the file's raw contents.
//
// It does not read the file — the caller does, so that a missing file is
// reported once, by the code that knows why it was being opened.
func ExtractDocument(path string, data []byte) (Document, error) {
	return ExtractDocumentAs(path, strings.ToLower(filepath.Ext(path)), data)
}

// ExtractDocumentAs is ExtractDocument for bytes that arrive without a usable
// name — content piped in, where there is no extension to read the format off.
//
// The format is spelled the way an extension is (".md", ".yaml", ".json"),
// because it answers the identical question and one concept should not have two
// vocabularies. A leading dot is optional: "md" and ".md" are the same answer.
//
// path is still carried, since every message names a file and a reader needs
// something to look at — a caller reading stdin passes the real destination
// where it knows one, and a placeholder where it does not.
func ExtractDocumentAs(path, format string, data []byte) (Document, error) {
	ext := strings.ToLower(format)
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	switch ext {
	case ".md":
		front, _, err := splitFrontmatter(data)
		if err != nil {
			// The split's own message says what is wrong with the fences; the
			// file name is prepended because that message does not carry it and
			// a hook's reader needs it.
			return Document{}, fmt.Errorf("%s: %w", path, err)
		}
		// The body is deliberately dropped. It is prose — and for a judging rule
		// the rubric itself — and it is not the schema's business.
		return Document{
			Path: path,
			Data: front,
			// The opening fence is line 1, so the frontmatter's first line is the
			// file's line 2.
			LineOffset: 1,
			Format:     "yaml",
		}, nil
	case ".yaml", ".yml":
		return Document{Path: path, Data: data, LineOffset: 0, Format: "yaml"}, nil
	case ".json":
		return Document{Path: path, Data: data, LineOffset: 0, Format: "json"}, nil
	default:
		return Document{}, &ErrUnknownExtension{Path: path, Ext: ext}
	}
}
