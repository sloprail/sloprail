package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/commandmod"
	"github.com/sloprail/sloprail/internal/transcript"
)

// The resolve-mode allowlist is what decides that a line is RUN before any rule
// has judged it, so it is tested as the pair the hook uses: OnlyCalls with
// isSRFileCall.
func TestResolveModeRunsOnlyGroundedVerbs(t *testing.T) {
	for _, src := range []string{
		`sr-file write a.md --content x`,
		`sr file edit a.md --old-string x --new-string y && echo ok`,
		`sr-file delete a.md || true; :`,
		`"sr-file" delete a.md`,
	} {
		assert.True(t, commandmod.OnlyCalls(src, isSRFileCall), "%s should be resolved", src)
	}
	for name, src := range map[string]string{
		"relative program path":       `./sr-file write a.md --content x`,
		"absolute program path":       `/tmp/x/sr-file write a.md --content x`,
		"proxy by path":               `./sr file write a.md --content x`,
		"a verb that ignores resolve": `sr-file validate a.md --schema s.cue`,
		"declarations":                `sr file declarations .`,
		"verb from a glob":            `sr-file ?rite a.md --content x`,
		"no verb":                     `sr-file`,
		"proxy with no verb":          `sr file`,
		"another service":             `sr session start`,
		"printf -v":                   `printf -v 'a[$(id)]' x && sr-file delete a.md`,
		"printf via glob":             `printf ?v 'a[$(id)]' x && sr-file delete a.md`,
		"resolve dir cleared":         `SR_FILE_RESOLVE_DIR= sr-file write a.md --content x`,
		"subbin dir swapped":          `SLOP_SUBBIN_DIR=. sr file write a.md --content x`,
		"cite chain":                  `sr-session trajectory cite 'q' && sr-file delete a.md`,
	} {
		assert.False(t, commandmod.OnlyCalls(src, isSRFileCall), "%s: %s must never run ahead of time", name, src)
	}
}

// A resolve-mode record's citations are kept only as far as the session's own
// record grounds them: a forged one — a quote the user never said, a line that
// does not hold it, a trajectory of the agent's choosing (the same line of
// another file is another entry) — is dropped, and a kept one is re-read from
// the session's record.
func TestRegroundTrustsOnlyTheSessionsRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(
		`{"type":"user","uuid":"u1","parentUuid":null,"message":{"role":"user","content":"keep a decision log"}}`+"\n"), 0o644))
	user := []transcript.SourceType{transcript.SourceUser}

	got := reground(citeRecord{Path: path}, []transcript.Citation{
		{Quote: "decision log", SourceTypes: user, Path: path, Line: 1, Message: "forged context"},
		{Quote: "decision log", SourceTypes: user, Path: "/tmp/fake.jsonl", Line: 1},
		{Quote: "never said", SourceTypes: user, Path: path, Line: 1},
		{Quote: "decision log", SourceTypes: user, Path: path, Line: 7},
	})
	require.Len(t, got, 1)
	assert.Equal(t, path, got[0].Path)
	assert.Equal(t, "keep a decision log", got[0].Message)

	assert.Empty(t, reground(citeRecord{}, []transcript.Citation{{Quote: "decision log", SourceTypes: user, Line: 1}}),
		"with no session record nothing is grounded")
}
