package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractDocument_MarkdownTakesFrontmatterOnly(t *testing.T) {
	src := []byte("---\ntitle: A note\nstatus: draft\n---\n\nProse below, which is not the schema's business.\n")

	doc, err := ExtractDocument("note.md", src)
	require.NoError(t, err)

	assert.Equal(t, "note.md", doc.Path)
	assert.Equal(t, "yaml", doc.Format)
	assert.Equal(t, "title: A note\nstatus: draft\n", string(doc.Data))
	assert.NotContains(t, string(doc.Data), "Prose below",
		"the body must not reach the schema — handing prose to a schema checker gets an error about the prose")
}

// The offset is the whole reason a position reported inside frontmatter lands on
// the right line of the file. Line 1 is the opening fence, so document line N is
// file line N+1.
func TestExtractDocument_MarkdownLineOffsetIsOneForTheFence(t *testing.T) {
	doc, err := ExtractDocument("note.md", []byte("---\na: 1\n---\nbody\n"))
	require.NoError(t, err)
	assert.Equal(t, 1, doc.LineOffset)
}

func TestExtractDocument_MarkdownWithNoFrontmatter(t *testing.T) {
	_, err := ExtractDocument("note.md", []byte("Just prose. No fence anywhere.\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "note.md", "the message must name the file")
	assert.Contains(t, err.Error(), "no frontmatter")
}

func TestExtractDocument_MarkdownWithUnterminatedFrontmatter(t *testing.T) {
	_, err := ExtractDocument("note.md", []byte("---\na: 1\nnever closed\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "note.md")
	assert.Contains(t, err.Error(), "unterminated")
}

func TestExtractDocument_YAMLIsTheWholeFile(t *testing.T) {
	src := []byte("a: 1\nb: two\n")
	for _, name := range []string{"config.yaml", "config.yml"} {
		t.Run(name, func(t *testing.T) {
			doc, err := ExtractDocument(name, src)
			require.NoError(t, err)
			assert.Equal(t, "yaml", doc.Format)
			assert.Equal(t, string(src), string(doc.Data))
			assert.Equal(t, 0, doc.LineOffset, "nothing sits above a whole-file document")
		})
	}
}

func TestExtractDocument_JSONIsTheWholeFile(t *testing.T) {
	src := []byte(`{"a": 1}`)
	doc, err := ExtractDocument("config.json", src)
	require.NoError(t, err)
	assert.Equal(t, "json", doc.Format)
	assert.Equal(t, string(src), string(doc.Data))
	assert.Equal(t, 0, doc.LineOffset)
}

func TestExtractDocument_UnknownExtensionIsRefusedNotGuessed(t *testing.T) {
	_, err := ExtractDocument("notes.txt", []byte("anything"))
	require.Error(t, err)

	var unknown *ErrUnknownExtension
	require.ErrorAs(t, err, &unknown, "callers branch on the class of fault, not its wording")
	assert.Equal(t, ".txt", unknown.Ext)
	assert.Contains(t, err.Error(), "notes.txt")
}

func TestExtractDocument_ExtensionMatchIsCaseInsensitive(t *testing.T) {
	doc, err := ExtractDocument("NOTE.MD", []byte("---\na: 1\n---\nbody\n"))
	require.NoError(t, err)
	assert.Equal(t, "yaml", doc.Format)
	assert.Equal(t, 1, doc.LineOffset)
}

func TestExtractDocument_NoExtensionAtAll(t *testing.T) {
	_, err := ExtractDocument("Makefile", []byte("x"))
	require.Error(t, err)
	var unknown *ErrUnknownExtension
	require.ErrorAs(t, err, &unknown)
	assert.Equal(t, "", unknown.Ext)
}
