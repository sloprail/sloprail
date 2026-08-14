package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(path, body string) error { return os.WriteFile(path, []byte(body), 0o644) }

// The two additions this file pins, both asked for by a rule author who was
// working around their absence:
//
//  1. STDIN. A hook holding pending content — bytes that are not on disk,
//     because the write has not happened yet — had to mktemp a directory, write
//     the bytes into it under a name ending in .md, and hand over the path.
//     That is three failure modes (mktemp, the macOS suffix trap, cleanup) in
//     service of handing over bytes the caller already had.
//
//  2. EMITTING THE VALIDATED DOCUMENT. sr-file has just parsed the frontmatter
//     in order to check it. A caller that then wants a field out of it — to
//     check the path it names actually resolves — was left to parse the same
//     bytes a second time, in a second language, with a second idea of what the
//     document is.
//
// Both are checked through runValidate rather than against the helpers, because
// what is being added is a command-line contract: the flags, what goes to
// stdout, and what the exit status is.

// runCmd drives the real cobra command the way a hook does, returning what it
// wrote to each stream.
func runCmd(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newValidateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func writeSchema(t *testing.T, src string) string {
	t.Helper()
	path := t.TempDir() + "/schema.cue"
	require.NoError(t, writeFile(path, src))
	return path
}

const stdinSchema = `
transcript_path!: string & !=""
`

// The plain case: bytes arrive on stdin, --as says how to read them, and a
// conforming document exits zero. No file is created anywhere.
func TestValidate_StdinAcceptsAConformingDocument(t *testing.T) {
	schema := writeSchema(t, stdinSchema)
	_, _, err := runCmd(t,
		"---\ntranscript_path: /tmp/session.jsonl\n---\n\nProse.\n",
		"-", "--as", ".md", "--schema", schema)
	assert.NoError(t, err)
}

// And it must still REFUSE through the same path — a stdin mode that passed
// everything would be worse than the tempfile it replaces.
func TestValidate_StdinRefusesAViolatingDocument(t *testing.T) {
	schema := writeSchema(t, stdinSchema)
	_, stderr, err := runCmd(t,
		"---\ntranscript_path: \"\"\n---\n\nProse.\n",
		"-", "--as", ".md", "--schema", schema)
	require.Error(t, err)
	assert.Contains(t, stderr+err.Error(), "transcript_path")
}

// WHY --as EXISTS. The extension is what decides which bytes are the document,
// and stdin has no name to carry one. Without an explicit answer the command
// would have to guess a splitter for bytes whose shape it does not know — the
// exact failure document.go was written to prevent — so the flag is REQUIRED
// with stdin rather than defaulted to .md.
func TestValidate_StdinWithoutAsIsRefusedRatherThanGuessed(t *testing.T) {
	schema := writeSchema(t, stdinSchema)
	_, _, err := runCmd(t,
		"---\ntranscript_path: /tmp/s.jsonl\n---\n",
		"-", "--schema", schema)
	require.Error(t, err, "reading stdin with no --as must not guess a format")
	assert.Contains(t, err.Error(), "--as")
}

// --as takes the same vocabulary the extension does, spelled the same way, so
// there is one set of names for one concept. A leading dot is optional because
// "md" and ".md" are the same answer to the same question.
func TestValidate_AsAcceptsExtensionSpellingWithOrWithoutDot(t *testing.T) {
	schema := writeSchema(t, stdinSchema)
	for _, as := range []string{".md", "md", ".MD"} {
		_, _, err := runCmd(t,
			"---\ntranscript_path: /tmp/s.jsonl\n---\n",
			"-", "--as", as, "--schema", schema)
		assert.NoErrorf(t, err, "--as %q should name the markdown document", as)
	}
}

// An --as naming a format nothing can split is an error, for the same reason an
// unknown extension is: guessing produces a complaint about the wrong bytes.
func TestValidate_AsWithAnUnknownFormatIsRefused(t *testing.T) {
	schema := writeSchema(t, stdinSchema)
	_, _, err := runCmd(t, "whatever", "-", "--as", ".txt", "--schema", schema)
	require.Error(t, err)
	assert.Contains(t, err.Error(), ".txt")
}

// A whole-file format through stdin, to prove --as is not markdown-only.
func TestValidate_StdinReadsAWholeFileFormat(t *testing.T) {
	schema := writeSchema(t, stdinSchema)
	_, _, err := runCmd(t, "transcript_path: /tmp/s.jsonl\n",
		"-", "--as", ".yaml", "--schema", schema)
	assert.NoError(t, err)
}

// THE SECOND ADDITION. On success --emit prints the validated document as JSON,
// so the caller can pull a field out with jq instead of re-parsing the bytes.
func TestValidate_EmitPrintsTheValidatedDocumentAsJSON(t *testing.T) {
	schema := writeSchema(t, stdinSchema)
	stdout, _, err := runCmd(t,
		"---\ntranscript_path: /tmp/session.jsonl\nextra: kept\n---\n\nProse.\n",
		"-", "--as", ".md", "--schema", schema, "--emit")
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got),
		"a caller pipes this into jq, so it has to be one JSON value")
	assert.Equal(t, "/tmp/session.jsonl", got["transcript_path"])
	assert.Equal(t, "kept", got["extra"],
		"the schema is open, so a field it does not mention is still part of the document")
}

// Nothing is emitted for a document that FAILED. Printing a document that did
// not satisfy the schema would let `validate --emit | jq` read fields out of a
// file the command just refused, with the exit status the only thing saying so.
func TestValidate_EmitPrintsNothingOnFailure(t *testing.T) {
	schema := writeSchema(t, stdinSchema)
	stdout, _, err := runCmd(t,
		"---\ntranscript_path: \"\"\n---\n",
		"-", "--as", ".md", "--schema", schema, "--emit")
	require.Error(t, err)
	assert.Empty(t, strings.TrimSpace(stdout), "a refused document must not be emitted")
}

// Without --emit stdout stays empty, so an existing caller reading exit status
// alone is unaffected by this addition.
func TestValidate_WithoutEmitStdoutStaysSilent(t *testing.T) {
	schema := writeSchema(t, stdinSchema)
	stdout, _, err := runCmd(t,
		"---\ntranscript_path: /tmp/s.jsonl\n---\n",
		"-", "--as", ".md", "--schema", schema)
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(stdout))
}

// --emit works off a real file too: the two input routes differ in where the
// bytes come from and in nothing else.
func TestValidate_EmitWorksForAPathAsWellAsStdin(t *testing.T) {
	schema := writeSchema(t, stdinSchema)
	doc := t.TempDir() + "/DECISION.md"
	require.NoError(t, writeFile(doc, "---\ntranscript_path: /tmp/from-file.jsonl\n---\n\nBody.\n"))

	stdout, _, err := runCmd(t, "", doc, "--schema", schema, "--emit")
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "/tmp/from-file.jsonl", got["transcript_path"])
}

// --as is about bytes with no name, so it must not quietly override a real
// file's own extension — that would be two answers to one question.
func TestValidate_AsIsRejectedAlongsideAPath(t *testing.T) {
	schema := writeSchema(t, stdinSchema)
	doc := t.TempDir() + "/DECISION.md"
	require.NoError(t, writeFile(doc, "---\ntranscript_path: /tmp/x.jsonl\n---\n"))

	_, _, err := runCmd(t, "", doc, "--as", ".yaml", "--schema", schema)
	require.Error(t, err, "a named file already says what it is")
	assert.Contains(t, err.Error(), "--as")
}
