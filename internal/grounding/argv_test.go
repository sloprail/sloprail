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

// The Edit tool's own parameter names spell the same flags.
func TestParseFileEditToolSpellings(t *testing.T) {
	fc, err := ParseFile([]string{"edit", "a.md", "--old_string", "x", "--new_string=y", "--replace_all", "--cite:tool_result", "PASS"})
	require.NoError(t, err)
	assert.Equal(t, FileCommand{
		Verb: VerbEdit, Path: "a.md", OldString: "x", NewString: "y", ReplaceAll: true,
		Cites: []transcript.CitationRequest{{Quote: "PASS", SourceTypes: toolResult}},
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
		"replace-all word":   {"edit", "a.md", "--old-string", "x", "--new-string", "y", "--replace-all", "false"},
		"content twice":      {"write", "a.md", "--content", "x", "--content", "y"},
		"old twice, aliased": {"edit", "a.md", "--old-string", "x", "--old_string", "z", "--new-string", "y"},
		"new twice, inline":  {"edit", "a.md", "--old-string", "x", "--new-string=y", "--new-string", "w"},
		"empty pool":         {"delete", "a.md", "--cite:", "x"},
		"comma-only pool":    {"delete", "a.md", "--cite:,", "x"},
		"uppercase pool":     {"delete", "a.md", "--cite:USER", "x"},
		"cite without colon": {"delete", "a.md", "--cite", "x"},
		"underscored cite":   {"delete", "a.md", "--cite_user", "x"},
		"unknown flag":       {"write", "a.md", "--force"},
		"unknown short flag": {"write", "a.md", "-f"},
		"dangling content":   {"write", "a.md", "--content"},
		"path after --":      {"delete", "a.md", "--", "b.md"},
		"flag after --":      {"delete", "--", "a.md", "--cite:user", "q"},
		"empty path":         {"delete", ""},
		"verb case":          {"Write", "a.md"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseFile(args)
			require.Error(t, err)
		})
	}
}

func TestParseFileHelp(t *testing.T) {
	for _, args := range [][]string{{"edit", "--help"}, {"-h"}, {"write", "a.md", "-h"}} {
		_, err := ParseFile(args)
		assert.True(t, errors.Is(err, ErrHelp), "%v", args)
	}
	// A help flag consumed as a value is a value.
	fc, err := ParseFile([]string{"write", "a.md", "--content", "-h"})
	require.NoError(t, err)
	assert.Equal(t, "-h", fc.Content)
}

// Values are taken verbatim — the shell has already done its quoting — whether
// they look like flags, hold `=`, or span lines.
func TestParseFileValues(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want FileCommand
	}{
		"value starting with a dash": {
			[]string{"edit", "a.md", "--old-string", "-x", "--new-string", "--"},
			FileCommand{Verb: VerbEdit, Path: "a.md", OldString: "-x", NewString: "--", Cites: nil},
		},
		"inline value holding =": {
			[]string{"write", "a.md", "--content=k=v", "--cite:user=a = b"},
			FileCommand{Verb: VerbWrite, Path: "a.md", Content: "k=v", HasContent: true,
				Cites: []transcript.CitationRequest{{Quote: "a = b", SourceTypes: user}}},
		},
		"path after flags, with spaces": {
			[]string{"delete", "--cite:user", "q", "dir with space/a.md"},
			FileCommand{Verb: VerbDelete, Path: "dir with space/a.md",
				Cites: []transcript.CitationRequest{{Quote: "q", SourceTypes: user}}},
		},
		"absolute and escaping paths are the command's to judge": {
			[]string{"delete", "../../etc/x"},
			FileCommand{Verb: VerbDelete, Path: "../../etc/x"},
		},
		"repeated cites, a pool named twice counts once": {
			[]string{"delete", "a.md", "--cite:user,user", "q1", "--cite:tool_result,user", "q2"},
			FileCommand{Verb: VerbDelete, Path: "a.md", Cites: []transcript.CitationRequest{
				{Quote: "q1", SourceTypes: user},
				{Quote: "q2", SourceTypes: []transcript.SourceType{transcript.SourceToolResult, transcript.SourceUser}},
			}},
		},
		"multi-line content": {
			[]string{"write", "a.md", "--content", "a\n\tb\n"},
			FileCommand{Verb: VerbWrite, Path: "a.md", Content: "a\n\tb\n", HasContent: true},
		},
		"replace-all=false": {
			[]string{"edit", "a.md", "--old-string", "x", "--new-string", "y", "--replace-all=false"},
			FileCommand{Verb: VerbEdit, Path: "a.md", OldString: "x", NewString: "y"},
		},
		"a lone dash is a path": {
			[]string{"write", "-", "--content", "x"},
			FileCommand{Verb: VerbWrite, Path: "-", Content: "x", HasContent: true},
		},
	} {
		t.Run(name, func(t *testing.T) {
			fc, err := ParseFile(tc.args)
			require.NoError(t, err)
			assert.Equal(t, tc.want, fc)
		})
	}
}

func TestParseCite(t *testing.T) {
	req, err := ParseCite([]string{"--source-types", "user,tool_result", "--path", "/x.jsonl", "--include-envelope", "the ask"})
	require.NoError(t, err)
	assert.Equal(t, transcript.CitationRequest{Quote: "the ask", SourceTypes: both}, req)

	req, err = ParseCite([]string{"the ask"})
	require.NoError(t, err)
	assert.Equal(t, user, req.SourceTypes, "cite defaults to the user pool")

	req, err = ParseCite([]string{"--source-types=tool_result", "--", "-5 degrees"})
	require.NoError(t, err)
	assert.Equal(t, transcript.CitationRequest{Quote: "-5 degrees", SourceTypes: toolResult}, req)

	for name, args := range map[string][]string{
		"two quotes":        {"a", "b"},
		"no quote":          {},
		"empty quote":       {""},
		"dangling pools":    {"q", "--source-types"},
		"unknown pool":      {"--source-types", "assistant", "q"},
		"empty pools":       {"--source-types=", "q"},
		"help":              {"--help", "q"},
		"unknown flag":      {"-s", "user", "q"},
		"dash-leading word": {"-5 degrees"},
	} {
		_, err := ParseCite(args)
		assert.Error(t, err, name)
	}
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
		{"sr", "file", "write", "--help"},
		{"sr", "trajectory", "cite", "q"},
		{"sr"},
		{"sr-session", "cite", "q"},
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
	in := []transcript.Citation{{Quote: "q", SourceTypes: both, Path: "/s.jsonl", Line: 7, Message: "the whole q message", Call: "Bash: go test ./..."}}
	assert.Equal(t, in, FromWire(ToWire(in)))
	assert.NotNil(t, ToWire(nil), "no citations is an empty list, never null")
	assert.Empty(t, FromWire([]any{map[string]any{"quote": "q"}}), "an entry missing its location is not a citation")
}

func TestTargetOfIgnoresValues(t *testing.T) {
	fc, ok := TargetOf([]string{"edit", "a.md", "--old-string", "", "--new-string", "", "--cite:user", ""})
	require.True(t, ok, "values a static reader blanked do not hide the target")
	assert.Equal(t, VerbEdit, fc.Verb)
	assert.Equal(t, "a.md", fc.Path)
	assert.Equal(t, []transcript.CitationRequest{{Quote: "", SourceTypes: user}}, fc.Cites,
		"a blanked quote is kept blank, which grounds nothing")

	_, ok = TargetOf([]string{"edit", "--old-string", "x"})
	assert.False(t, ok)
}
