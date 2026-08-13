package transcript

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// write puts a transcript on disk and returns its path.
func write(t *testing.T, name string, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

func TestBelongsToSession_AFileNamingAnotherSessionIsRefused(t *testing.T) {
	// The case a guessed filename has to be checked against. A name colliding
	// with another conversation's transcript otherwise resolves silently and
	// hands back that conversation's identity.
	path := write(t, "guessed.jsonl",
		`{"type":"user","uuid":"THEIRS","parentUuid":null,"sessionId":"someone-else"}`)

	ok, err := BelongsToSession(path, "guessed")
	assert.False(t, ok)
	assert.ErrorIs(t, err, ErrWrongSession)
}

func TestBelongsToSession_AFileNamingThisSessionIsAccepted(t *testing.T) {
	path := write(t, "mine.jsonl",
		`{"type":"user","uuid":"MINE","parentUuid":null,"sessionId":"mine"}`)

	ok, err := BelongsToSession(path, "mine")
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestBelongsToSession_TheFirstRecordCarryingAnIDDecides(t *testing.T) {
	// Claude Code writes preamble records that carry a sessionId and no uuid, so
	// the check must not require a uuid — and it stops at the first id it finds
	// rather than reading a conversation of thousands to answer.
	path := write(t, "x.jsonl",
		`{"type":"summary","sessionId":"someone-else"}`,
		`{"type":"user","uuid":"U","parentUuid":null,"sessionId":"someone-else"}`)

	ok, err := BelongsToSession(path, "x")
	assert.False(t, ok)
	assert.ErrorIs(t, err, ErrWrongSession)
}

func TestBelongsToSession_ARecordCarryingNoIDIsAccepted(t *testing.T) {
	// The limit of the check, and deliberate. The field is observed rather than
	// promised: a harness that stops writing it must not turn every session into
	// a refusal. What is caught is a file naming a DIFFERENT session — a positive
	// disagreement — and what is not caught is a file naming no session at all.
	path := write(t, "x.jsonl",
		`{"type":"user","uuid":"U","parentUuid":null}`)

	ok, err := BelongsToSession(path, "x")
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestBelongsToSession_AMissingFileIsAccepted(t *testing.T) {
	// A guess landing on nothing was always the case that failed loudly, and it
	// fails at the read rather than here — where the error names the path. Also
	// the SessionStart case, where the harness may not have written the record
	// yet and refusing would cost the session its baseline.
	ok, err := BelongsToSession(filepath.Join(t.TempDir(), "absent.jsonl"), "absent")
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestBelongsToSession_NoSessionIDToCheckAgainstIsAccepted(t *testing.T) {
	// Nothing was guessed, so there is nothing to check.
	path := write(t, "x.jsonl", `{"type":"user","uuid":"U","parentUuid":null,"sessionId":"whatever"}`)
	ok, err := BelongsToSession(path, "")
	assert.True(t, ok)
	assert.NoError(t, err)
}
