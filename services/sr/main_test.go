package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProxyShortsMatchTheServices: every one-line description in the dispatch
// table equals the Short the service declares for itself.
//
// The proxy's help holds a copy of each service's Short so that printing it
// does not cost five process spawns. A copy that can drift is the bug this repo
// keeps finding — a list the reader trusts, disagreeing with the one the code
// runs on. The drift here is quiet: `sr --help` would describe a command one
// way and the command would describe itself another, and nothing would notice.
// It had ALREADY happened for `file` and `mark` when this test was written.
//
// Read from the SOURCE rather than from the binary, which is unusual here and
// worth the sentence. A cobra root command never prints its own Short: `--help`
// prints Long, and Short is shown only by a PARENT listing its children. These
// services have no parent, so the string is not observable from outside the
// process at all. Building them and grepping their output cannot work.
//
// So this parses `Short:` out of each service's main.go. That is coupled to the
// source's shape, and it is checked: a service whose Short cannot be found
// fails the test rather than passing vacuously.
func TestProxyShortsMatchTheServices(t *testing.T) {
	root := moduleDir(t)

	for _, s := range services {
		t.Run(s.name, func(t *testing.T) {
			path := filepath.Join(root, "services", binaryName(s.name), "main.go")
			src, err := os.ReadFile(path)
			require.NoError(t, err)

			got, ok := rootShort(string(src))
			require.True(t, ok, "no `Short:` found in %s — this test can no longer tell whether the proxy agrees with it", path)

			assert.Equal(t, s.short, got,
				"the proxy describes `sr %s` as %q, but %s declares itself %q — one was changed without the other",
				s.name, s.short, binaryName(s.name), got)
		})
	}
}

// rootShort returns the first `Short: "..."` in a service's main.go, which is
// its root command's — newRoot is the first command constructed in each.
func rootShort(src string) (string, bool) {
	const marker = `Short:`
	i := strings.Index(src, marker)
	if i < 0 {
		return "", false
	}
	rest := src[i+len(marker):]
	open := strings.Index(rest, `"`)
	if open < 0 {
		return "", false
	}
	rest = rest[open+1:]
	close := strings.Index(rest, `"`)
	if close < 0 {
		return "", false
	}
	return rest[:close], true
}

// TestEveryServiceDirectoryIsInTheTable: a service that exists is a service the
// proxy can reach.
//
// A new binary under services/ that nobody added here is unreachable through
// `sr`, and the only symptom is its absence from a help screen — which reads
// exactly like a command that was never written.
//
// services/sr itself is excluded: it is the proxy, and it does not proxy to
// itself.
func TestEveryServiceDirectoryIsInTheTable(t *testing.T) {
	entries, err := filepath.Glob(filepath.Join(moduleDir(t), "services", "*"))
	require.NoError(t, err)

	inTable := map[string]bool{}
	for _, s := range services {
		inTable[binaryName(s.name)] = true
	}

	for _, e := range entries {
		name := filepath.Base(e)
		if name == "sr" {
			continue
		}
		assert.True(t, inTable[name],
			"services/%s builds a binary the `sr` proxy cannot reach — add it to the dispatch table in main.go", name)
	}
}

func moduleDir(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}
