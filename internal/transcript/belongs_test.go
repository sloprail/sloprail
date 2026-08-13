package transcript

import (
	"os"
	"path/filepath"
	"strings"
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

func TestBelongsToSession_APreambleRecordCarryingNoUUIDStillDecides(t *testing.T) {
	// Claude Code writes preamble records that carry a sessionId and no uuid, so
	// the check must not require a uuid to read an id off a record.
	path := write(t, "x.jsonl",
		`{"type":"summary","sessionId":"someone-else"}`,
		`{"type":"user","uuid":"U","parentUuid":null,"sessionId":"someone-else"}`)

	ok, err := BelongsToSession(path, "x")
	assert.False(t, ok)
	assert.ErrorIs(t, err, ErrWrongSession)
}

func TestBelongsToSession_AForkedTranscriptCarryingTheOldIDFirstIsAccepted(t *testing.T) {
	// F9, and the case the whole package exists for. Claude Code re-forks a
	// session and keeps writing into the file; the records already there keep the
	// id they were written under, so the OLD id comes first and this session's id
	// appears only from the fork point on. Reading the first id refuses exactly
	// the re-fork identity.go was written to survive.
	//
	// The shape is the real one: many records under the old id, a few under this
	// one, this session's id last and in the minority.
	lines := []string{`{"type":"summary","sessionId":"old-session"}`}
	for i := 0; i < 12; i++ {
		lines = append(lines,
			`{"type":"user","uuid":"OLD`+string(rune('a'+i))+`","parentUuid":null,"sessionId":"old-session"}`)
	}
	lines = append(lines,
		`{"type":"user","uuid":"NEW1","parentUuid":null,"sessionId":"forked"}`)
	path := write(t, "forked.jsonl", lines...)

	ok, err := BelongsToSession(path, "forked")
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestBelongsToSession_AFileNamingOtherSessionsAndNeverThisOneIsRefused(t *testing.T) {
	// A guess landing on a file that names sessions and never this one. Every id
	// in it disagrees, which is the positive disagreement the check exists for.
	path := write(t, "guessed.jsonl",
		`{"type":"summary","sessionId":"someone-else"}`,
		`{"type":"user","uuid":"A","parentUuid":null,"sessionId":"a-third-party"}`,
		`{"type":"user","uuid":"B","parentUuid":"A","sessionId":"someone-else"}`)

	ok, err := BelongsToSession(path, "guessed")
	assert.False(t, ok)
	assert.ErrorIs(t, err, ErrWrongSession)
}

func TestBelongsToSession_ThisSessionNamedInTheMiddleIsAccepted(t *testing.T) {
	// Kills M6 — "consult the LAST id instead of the first".
	//
	// This is the ONE shape that separates containment from any position rule.
	// The fork fixture does not: its records under this session's id happen to
	// sit at the end, so reading the last record accepts it by accident, and the
	// same is true of every real transcript on this machine. A refusal fixture
	// does not either: when no record names this session, first, last, and
	// containment all refuse together.
	//
	// So the id is put in the MIDDLE, with a foreign id both before and after it.
	// First-id refuses, last-id refuses, containment accepts — and containment is
	// right, because a file that names this session anywhere holds this session's
	// history. A harness that resumed an older session for a few turns and then
	// forked again writes exactly this.
	path := write(t, "middle.jsonl",
		`{"type":"summary","sessionId":"older-session"}`,
		`{"type":"user","uuid":"A","parentUuid":null,"sessionId":"older-session"}`,
		`{"type":"user","uuid":"B","parentUuid":"A","sessionId":"middle"}`,
		`{"type":"user","uuid":"C","parentUuid":"B","sessionId":"newer-session"}`)

	ok, err := BelongsToSession(path, "middle")
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestBelongsToSession_AFileWhoseFirstRecordHasNoIDButLaterOnesAreForeignIsRefused(t *testing.T) {
	// F10. The scan skips id-less records rather than stopping at one, so a file
	// whose leading records carry no id is still judged on the ids it does carry.
	//
	// Refusing is right. The reason an id-less file is accepted is absence of
	// evidence — nothing in it disagrees. This file is not that: it names a
	// session, and the session it names is not this one. A leading record without
	// an id is Claude Code's preamble, not a statement of ownership, so letting it
	// suppress the disagreement behind it would turn the check off for exactly the
	// files that do have something to say.
	path := write(t, "x.jsonl",
		`{"type":"user","uuid":"U","parentUuid":null}`,
		`{"type":"user","uuid":"V","parentUuid":"U","sessionId":"someone-else"}`)

	ok, err := BelongsToSession(path, "x")
	assert.False(t, ok)
	assert.ErrorIs(t, err, ErrWrongSession)
}

func TestBelongsToSession_AFileCarryingNoIDAtAllIsAccepted(t *testing.T) {
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

// TestBelongsToSession_AgainstEveryRealTranscript is the check against the thing
// itself rather than against a fixture built from what we believe about it.
//
// The fixture above encodes an understanding of the fork; this encodes nothing.
// It walks whatever transcripts are actually on this machine and asserts the
// property the function now rests on — a transcript names its own filename stem
// somewhere in it — which is the property that survived a sweep of 8085 real
// files when "every record names the stem" and "a transcript is single-valued in
// sessionId" both did not.
//
// Skipped where there are no transcripts, so the suite still runs on a machine
// that has never run Claude Code. That makes it a check that can go quiet, which
// is why it is the companion to the fixtures and not a replacement for them.
func TestBelongsToSession_AgainstEveryRealTranscript(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join(ConfigDir(), "projects", "*", "*.jsonl"))
	if len(paths) == 0 {
		t.Skip("no real transcripts on this machine")
	}

	refused := 0
	for _, p := range paths {
		stem := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		ok, err := BelongsToSession(p, stem)
		if !ok {
			refused++
			t.Errorf("refused a real transcript under its own name: %v", err)
		}
		_ = err
	}
	t.Logf("checked %d real transcripts, %d refused", len(paths), refused)
}
