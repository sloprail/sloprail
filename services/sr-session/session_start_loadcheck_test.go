package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The load check's closing line (reportLoadCheck): the number of rules that
// loaded, the ones that could not, and only when there is no session. Its count
// is what tells an author the rules they wrote were read at all, and a harness's
// SessionStart has no reader for it.

// loadCheckTree is a project with two rules that load (a gate and a file-guard)
// and, when broken is set, a file-guard that cannot (its match names a field the
// file scope does not carry).
func loadCheckTree(t *testing.T, broken bool) string {
	t.Helper()
	tree := initRepo(t)
	write := func(rel, body string) {
		p := filepath.Join(tree, ".sloprail", rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o755))
	}
	write("gate/on-stop/gate.yaml", "on:\n  - event: Stop\nchecks:\n  - script: ./check.sh\n")
	write("gate/on-stop/check.sh", "#!/bin/sh\nexit 0\n")
	write("file-guard/notes/file-guard.yaml", "match: 'path matches \"notes/.*\"'\nchecks:\n  - script: ./check.sh\n")
	write("file-guard/notes/check.sh", "#!/bin/sh\nexit 0\n")
	if broken {
		write("file-guard/typo/file-guard.yaml", "match: marker.kind == \"endpoint\"\nchecks:\n  - script: ./check.sh\n")
		write("file-guard/typo/check.sh", "#!/bin/sh\nexit 0\n")
	}
	return tree
}

func TestSessionStart_LoadCheckCountsTheRules(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	_, stderr := runHook(t, newSessionStartCmd(), HookPayload{Cwd: loadCheckTree(t, false)})
	assert.Contains(t, stderr, "sloprail: 2 rules loaded. This only checked that they load",
		"the load check must count the rules that loaded")

	_, stderr = runHook(t, newSessionStartCmd(), HookPayload{Cwd: loadCheckTree(t, true)})
	assert.Contains(t, stderr, "sloprail: 2 rules loaded, 1 could not load (above).",
		"a rule that could not load is counted apart from the ones that did")
}

// A start that carries a session is a harness starting one, not the load check:
// it prints no closing line.
func TestSessionStart_SessionPrintsNoLoadCheckLine(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	tree := loadCheckTree(t, false)

	_, stderr := runHook(t, newSessionStartCmd(), HookPayload{
		TranscriptPath: filepath.Join(t.TempDir(), "fresh-session.jsonl"),
		SessionID:      "fresh-session",
		Cwd:            tree,
	})
	assert.NotContains(t, stderr, "rules loaded", "a session's start printed the load check's line")
}
