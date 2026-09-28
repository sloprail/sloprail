package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `sr-file field` reads ONE top-level field of a document with a plain YAML
// reader — no schema, no conversion to JSON — so a caller can ask "what status
// does this unit claim?" of YAML that is valid but that a schema, or a JSON
// view of it, would reject (an integer key, a custom tag, `.inf`).

func runField(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := newFieldCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestField_ReadsTheFieldOfValidYAMLASchemaWouldReject(t *testing.T) {
	for name, doc := range map[string]string{
		"plain":                  "---\nstatus: published\n---\n",
		"integer key":            "---\n1: x\nstatus: published\n---\n",
		"null key":               "---\nnull: x\nstatus: published\n---\n",
		"complex key":            "---\n? [a, b]\n: c\nstatus: published\n---\n",
		"custom tag":             "---\nx: !custom foo\nstatus: published\n---\n",
		"huge int":               "---\nn: 123456789012345678901234567890\nstatus: published\n---\n",
		"inf and nan":            "---\na: .inf\nb: .nan\nstatus: published\n---\n",
		"value on the next line": "---\nstatus:\n  published\n---\n",
		"explicit key":           "---\n? status\n: published\n---\n",
		"escaped key and value":  "---\n\"stat\\x75s\": \"pub\\x6cished\"\n---\n",
		"alias":                  "---\ns: &s published\nstatus: *s\n---\n",
		"merge key":              "---\nbase: &b {status: published}\n<<: *b\n---\n",
		"nbsp after the fence":   "---\u00a0\nstatus: published\n---\n",
	} {
		t.Run(name, func(t *testing.T) {
			out, err := runField(t, doc, "-", "status", "--as", ".md")
			require.NoError(t, err)
			assert.Equal(t, "published\n", out)
		})
	}
}

func TestField_AbsentOrNonScalarPrintsNothing(t *testing.T) {
	for _, doc := range []string{
		"---\ntype: post\n---\n",
		"---\nstatus: [published]\n---\n",
		"---\n# only a comment\n---\n",
		"---\n- a list\n---\n",
	} {
		out, err := runField(t, doc, "-", "status", "--as", ".md")
		require.NoError(t, err, doc)
		assert.Equal(t, "", out, doc)
	}
}

// No frontmatter — no opening fence — is its own answer, distinct from a
// document that does not parse: the caller reads "no status" from it, not
// "unreadable". (An opening fence never closed is exit 3, below.)
func TestField_NoFrontmatterIsItsOwnError(t *testing.T) {
	for _, doc := range []string{
		"Just prose.\nstatus: published\n",
		"\xef\xbb\xbf---\nstatus: published\n---\n",
	} {
		_, err := runField(t, doc, "-", "status", "--as", ".md")
		require.Error(t, err, doc)
		assert.True(t, errors.Is(err, errNoFrontmatter), "want no-frontmatter for %q, got %v", doc, err)
	}
}

// A document that opens a frontmatter and does not parse — or defines the field
// twice — is unreadable, and says so: never "no frontmatter", never a value.
func TestField_UnreadableIsNotNoFrontmatter(t *testing.T) {
	for _, doc := range []string{
		"---\ntype: [\nstatus: published\n---\n",
		" ---\ntype: [\n---\n",
		"---\nstatus: drafting\nstatus: published\n---\n",
		// A directive (%TAG, %YAML) must be followed by a `---` document start,
		// and inside frontmatter that line would be the closing fence — so a
		// directive in frontmatter is a YAML syntax error, not valid YAML.
		"---\n%TAG !e! tag:example.com,2026:\nx: !e!thing y\nstatus: drafting\n---\n",
	} {
		out, err := runField(t, doc, "-", "status", "--as", ".md")
		require.Error(t, err, doc)
		assert.False(t, errors.Is(err, errNoFrontmatter), "an unreadable document is not a missing one: %q", doc)
		assert.Equal(t, "", out)
	}
}

// exitOf is the exit status main would use for err.
func exitOf(err error) int { return exitStatus(err) }

// A merge chain that fans out — each level merging the previous one eight
// times — is read in linear time: each mapping is searched once per lookup.
func TestField_MergeFanOutIsLinear(t *testing.T) {
	var b strings.Builder
	b.WriteString("---\nl0: &l0 {a: 1}\n")
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&b, "l%d: &l%d\n  <<: [", i, i)
		for j := 0; j < 8; j++ {
			if j > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "*l%d", i-1)
		}
		b.WriteString("]\n")
	}
	b.WriteString("<<: *l30\n---\n")
	start := time.Now()
	out, err := runField(t, b.String(), "-", "status", "--as", ".md")
	require.NoError(t, err)
	assert.Equal(t, "", out)
	assert.Less(t, time.Since(start), time.Second, "a fanned-out merge chain must not be re-searched per path")
}

// An opening fence never closed is its own answer (exit 3), distinct from no
// fence at all (exit 2): the text after it may be a frontmatter someone forgot
// to close, or prose under a horizontal rule — the caller reads it to decide.
func TestField_UnterminatedFrontmatterIsItsOwnStatus(t *testing.T) {
	for _, doc := range []string{
		"---\ntype: post\nstatus: published\n",
		"---\r\ntype: post\r\nstatus: published\r\n",
		"---\n\nA body that opens with a horizontal rule.\n",
	} {
		_, err := runField(t, doc, "-", "status", "--as", ".md")
		require.Error(t, err, doc)
		assert.Equal(t, fieldExitUnterminated, exitOf(err), "%q", doc)
	}
	_, err := runField(t, "Just prose.\n", "-", "status", "--as", ".md")
	assert.Equal(t, fieldExitNoFrontmatter, exitOf(err))
}

// A second YAML document, or a second merge key, is a document two readers can
// read two ways: unreadable, never the first one's answer.
func TestField_SecondDocumentOrMergeKeyIsUnreadable(t *testing.T) {
	for _, doc := range []string{
		"---\nstatus: drafting\n...\nstatus: published\n---\n",
		"---\n<<: {status: drafting}\n<<: {status: published}\n---\n",
	} {
		out, err := runField(t, doc, "-", "status", "--as", ".md")
		require.Error(t, err, doc)
		assert.Equal(t, fieldExitUnreadable, exitOf(err), "%q", doc)
		assert.Equal(t, "", out)
	}
}

// A usage error — an unknown command, a missing argument — has its own exit
// status, so a caller can tell "this sr-file cannot do that" from "the document
// is unreadable".
func TestExitStatus_UsageErrorsAreDistinct(t *testing.T) {
	for _, args := range [][]string{
		{"nosuch"}, {"field", "-"}, {"field", "-", "status", "--nosuchflag"},
		{"field", "-", "status"},                   // stdin with no --as
		{"field", "a.md", "status", "--as", ".md"}, // --as with a path
		{"field", "-", "status", "--as", ".txt"},   // --as naming no format
		{"validate", "-", "--schema", "/dev/null"}, // the same, through validate
		{"write"},                        // no file path
		{"edit", "a.md", "--old-string"}, // a flag with no value
		{"delete", "a.md", "--cite:nosuchpool", "quote"}, // a pool that does not exist
	} {
		root := newRoot()
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetIn(strings.NewReader(""))
		root.SetArgs(args)
		err := root.Execute()
		require.Error(t, err, args)
		assert.Equal(t, exitUsage, exitOf(err), "%v: %v", args, err)
	}
}

// Reading input never blocks and never runs away: a path that is not a regular
// file (a FIFO) and input over the cap are refused at once.
func TestReadInput_RefusesFIFOAndOversizedInput(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "unit.md")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))
	done := make(chan error, 1)
	go func() { _, err := runField(t, "", fifo, "status"); done <- err }()
	select {
	case err := <-done:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a regular file")
	case <-time.After(2 * time.Second):
		t.Fatal("reading a FIFO blocked")
	}

	big := "---\nstatus: drafting\n---\n" + strings.Repeat("x", maxInputBytes)
	_, err := runField(t, big, "-", "status", "--as", ".md")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "larger than")

	// The same cap on a file named by path.
	bigFile := filepath.Join(t.TempDir(), "big.md")
	require.NoError(t, os.WriteFile(bigFile, []byte(big), 0o644))
	_, err = runField(t, "", bigFile, "status")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "larger than")
}
