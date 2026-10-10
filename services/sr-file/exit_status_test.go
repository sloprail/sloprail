package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statusOf runs sr-file as main does, from the root command, and returns the status main
// would exit with.
func statusOf(t *testing.T, stdin string, args ...string) int {
	t.Helper()
	cmd := newRoot()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	if err == nil {
		return 0
	}
	return exitStatus(err)
}

// A document the schema refuses, and one that cannot be checked, exit 1; a command line
// sr-file could not use exits 64, so a hook tells the two apart by the status alone.
// sr:proves cli/file-validate-usage-is-not-a-refusal
func TestValidate_AUsageErrorIsNotARefusal(t *testing.T) {
	dir := t.TempDir()
	schema := filepath.Join(dir, "schema.cue")
	require.NoError(t, writeFile(schema, "title!: string & !=\"\"\n"))
	good := filepath.Join(dir, "good.yaml")
	require.NoError(t, writeFile(good, "title: a note\n"))
	bad := filepath.Join(dir, "bad.yaml")
	require.NoError(t, writeFile(bad, "title: \"\"\n"))
	broken := filepath.Join(dir, "broken.yaml")
	require.NoError(t, writeFile(broken, "title: [unclosed\n"))
	brokenSchema := filepath.Join(dir, "broken.cue")
	require.NoError(t, writeFile(brokenSchema, "title!: string &\n"))

	for name, c := range map[string]struct {
		args []string
		want int
	}{
		"a conforming document":                   {[]string{"validate", good, "--schema", schema}, 0},
		"a document that violates the schema":     {[]string{"validate", bad, "--schema", schema}, 1},
		"a document that cannot be parsed":        {[]string{"validate", broken, "--schema", schema}, 1},
		"a document that is not there":            {[]string{"validate", filepath.Join(dir, "gone.yaml"), "--schema", schema}, 1},
		"a schema that cannot be read":            {[]string{"validate", good, "--schema", brokenSchema}, 1},
		"an unknown flag":                         {[]string{"validate", good, "--schema", schema, "--no-such-flag"}, exitUsage},
		"no document named":                       {[]string{"validate", "--schema", schema}, exitUsage},
		"two documents named":                     {[]string{"validate", good, bad, "--schema", schema}, exitUsage},
		"stdin with no --as to say what it is":    {[]string{"validate", "-", "--schema", schema}, exitUsage},
		"--as beside a path that already says":    {[]string{"validate", good, "--as", ".yaml", "--schema", schema}, exitUsage},
		"an unknown flag on a violating document": {[]string{"validate", bad, "--schema", schema, "--no-such-flag"}, exitUsage},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, c.want, statusOf(t, "title: a note\n", c.args...))
		})
	}
}
