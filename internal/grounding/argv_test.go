package grounding

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/transcript"
)

var (
	user       = []transcript.SourceType{transcript.SourceUser}
	toolResult = []transcript.SourceType{transcript.SourceToolResult}
	both       = []transcript.SourceType{transcript.SourceUser, transcript.SourceToolResult}
)

func TestParseFileEdit(t *testing.T) {
	fc, err := ParseFile([]string{"edit", "a.md", "--old-string", "x", "--new-string=y", "--replace-all",
		"--cite:user", "the ask", "--cite:tool_result=PASS", "--cite:user,tool_result", "either"})
	require.NoError(t, err)
	assert.Equal(t, FileCommand{
		Verb: VerbEdit, Path: "a.md", OldString: "x", NewString: "y", ReplaceAll: true,
		Cites: []transcript.CitationRequest{
			{Quote: "the ask", SourceTypes: user},
			{Quote: "PASS", SourceTypes: toolResult},
			{Quote: "either", SourceTypes: both},
		},
	}, fc)
}

func TestParseFileWriteAndDelete(t *testing.T) {
	fc, err := ParseFile([]string{"write", "--content", "", "a.md"})
	require.NoError(t, err)
	assert.True(t, fc.HasContent, "an explicitly empty --content is a stated body")
	assert.Equal(t, "", fc.Content)

	fc, err = ParseFile([]string{"write", "a.md"})
	require.NoError(t, err)
	assert.False(t, fc.HasContent, "no --content means the body is on stdin")

	fc, err = ParseFile([]string{"delete", "--", "-odd-name.md"})
	require.NoError(t, err)
	assert.Equal(t, "-odd-name.md", fc.Path)
}

func TestParseFileRefusals(t *testing.T) {
	for name, args := range map[string][]string{
		"no verb":            {},
		"unknown verb":       {"move", "a.md"},
		"no path":            {"delete"},
		"two paths":          {"delete", "a.md", "b.md"},
		"edit missing new":   {"edit", "a.md", "--old-string", "x"},
		"edit empty old":     {"edit", "a.md", "--old-string", "", "--new-string", "y"},
		"edit no-op":         {"edit", "a.md", "--old-string", "x", "--new-string", "x"},
		"flag of other verb": {"delete", "a.md", "--content", "x"},
		"unknown pool":       {"delete", "a.md", "--cite:assistant", "x"},
		"empty quote":        {"delete", "a.md", "--cite:user", ""},
		"dangling value":     {"delete", "a.md", "--cite:user"},
		"bad replace-all":    {"edit", "a.md", "--old-string", "x", "--new-string", "y", "--replace-all=maybe"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseFile(args)
			require.Error(t, err)
		})
	}
}

func TestParseFileHelp(t *testing.T) {
	_, err := ParseFile([]string{"edit", "--help"})
	assert.True(t, errors.Is(err, ErrHelp))
}

func TestParseCite(t *testing.T) {
	req, err := ParseCite([]string{"--source-types", "user,tool_result", "--path", "/x.jsonl", "--include-envelope", "the ask"})
	require.NoError(t, err)
	assert.Equal(t, transcript.CitationRequest{Quote: "the ask", SourceTypes: both}, req)

	req, err = ParseCite([]string{"the ask"})
	require.NoError(t, err)
	assert.Equal(t, user, req.SourceTypes, "cite defaults to the user pool")

	_, err = ParseCite([]string{"a", "b"})
	require.Error(t, err)
}

func TestFromArgv(t *testing.T) {
	inv, ok, err := FromArgv([]string{"/usr/local/bin/sr-file", "delete", "a.md", "--cite:user", "q"})
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, inv.File)
	assert.Equal(t, "a.md", inv.File.Path)
	assert.Len(t, inv.Cites, 1)

	inv, ok, err = FromArgv([]string{"sr", "file", "delete", "a.md"})
	require.NoError(t, err)
	require.True(t, ok, "the sr proxy spelling is the same command")
	assert.Equal(t, VerbDelete, inv.File.Verb)

	for _, argv := range [][]string{
		{"sr-session", "trajectory", "cite", "q"},
		{"sr", "session", "trajectory", "cite", "q"},
	} {
		inv, ok, err = FromArgv(argv)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Nil(t, inv.File)
		assert.Equal(t, []transcript.CitationRequest{{Quote: "q", SourceTypes: user}}, inv.Cites)
	}

	for _, argv := range [][]string{
		{"rm", "a.md"},
		{"sr-session", "trajectory", "describe"},
		{"sr-file", "--help"},
	} {
		_, ok, err = FromArgv(argv)
		require.NoError(t, err)
		assert.False(t, ok, "%v carries no citation", argv)
	}

	_, ok, err = FromArgv([]string{"sr-file", "edit", "a.md"})
	assert.True(t, ok)
	assert.Error(t, err, "a malformed sr-file line is recognised and reported")
}

func TestWireRoundTrip(t *testing.T) {
	in := []transcript.Citation{{Quote: "q", SourceTypes: both, Path: "/s.jsonl", Line: 7}}
	assert.Equal(t, in, FromWire(ToWire(in)))
	assert.NotNil(t, ToWire(nil), "no citations is an empty list, never null")
	assert.Empty(t, FromWire([]any{map[string]any{"quote": "q"}}), "an entry missing its location is not a citation")
}

func TestTargetOfIgnoresValues(t *testing.T) {
	verb, path, ok := TargetOf([]string{"edit", "a.md", "--old-string", "", "--new-string", "", "--cite:user", ""})
	require.True(t, ok, "values a static reader blanked do not hide the target")
	assert.Equal(t, VerbEdit, verb)
	assert.Equal(t, "a.md", path)

	_, _, ok = TargetOf([]string{"edit", "--old-string", "x"})
	assert.False(t, ok)
}
