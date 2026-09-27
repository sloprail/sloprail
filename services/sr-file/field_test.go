package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

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

// No frontmatter — no opening fence, or an opening `---` that is a horizontal
// rule with no closing fence — is its own answer, distinct from a document that
// does not parse: the caller reads "no status" from it, not "unreadable".
func TestField_NoFrontmatterIsItsOwnError(t *testing.T) {
	for _, doc := range []string{
		"Just prose.\nstatus: published\n",
		"---\n\nA body that opens with a horizontal rule.\n",
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
