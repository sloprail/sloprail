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

// sessionRecord is an ordinary record carrying the two fields the guess checks
// look at: which session wrote it, and which tree it was written in.
func sessionRecord(uuid, sessionID, cwd string) string {
	return `{"type":"user","uuid":"` + uuid + `","parentUuid":null,"sessionId":"` + sessionID +
		`","cwd":"` + cwd + `","message":{"role":"user","content":"hi"}}`
}

func TestBelongsToTree_AcceptsItsOwnTree(t *testing.T) {
	// The tree check's positive case: the records were written in the directory
	// being asked about.
	dir := t.TempDir()
	path := write(t, "s-1.jsonl", sessionRecord("u-1", "s-1", dir))

	ok, err := BelongsToTree(path, dir)

	require.NoError(t, err)
	assert.True(t, ok)
}

func TestBelongsToTree_RefusesACollidingSiblingTree(t *testing.T) {
	// The collision BelongsToSession cannot see. EncodeProjectDir maps every
	// non-alphanumeric byte to "-", so a separator and a literal hyphen become
	// the same character: the subdirectory "<base>/proj/pkg" and the sibling
	// checkout "<base>/proj-pkg" encode identically. A guess from one lands on
	// the other's transcript, and that transcript's session id genuinely
	// matches — so only the recorded cwd distinguishes them.
	base := t.TempDir()
	sub := filepath.Join(base, "proj", "pkg")
	sibling := filepath.Join(base, "proj-pkg")
	require.Equal(t, EncodeProjectDir(sub), EncodeProjectDir(sibling),
		"the collision under test must actually collide")

	path := write(t, "s-1.jsonl", sessionRecord("u-1", "s-1", sibling))

	ok, err := BelongsToSession(path, "s-1")
	require.NoError(t, err)
	require.True(t, ok, "the session check agrees — the id really is that file's own")

	ok, err = BelongsToTree(path, sub)

	assert.False(t, ok, "same encoded directory, different tree")
	assert.ErrorIs(t, err, ErrWrongTree)
}

func TestBelongsToTree_AcceptsWhenAnyRecordMatches(t *testing.T) {
	// A session that cd's into a subdirectory writes records with several
	// distinct cwds — 57 of 8085 real transcripts do. Any one of them matching
	// is enough, so the match must not depend on which record comes first;
	// testing only the first would falsely refuse 7.
	base := t.TempDir()
	sub := filepath.Join(base, "src")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	// The first recorded cwd is the subdirectory; the tree being asked about
	// appears only later.
	path := write(t, "s-1.jsonl",
		sessionRecord("u-1", "s-1", sub),
		sessionRecord("u-2", "s-1", base))

	ok, err := BelongsToTree(path, base)

	require.NoError(t, err, "a later record carries the very tree asked about")
	assert.True(t, ok)
}

func TestBelongsToTree_AcceptsATreeBeneathTheOneAsked(t *testing.T) {
	// Beneath counts, because the guess is keyed on the project directory of a
	// tree and a session that cd'd into a subdirectory still belongs to it. The
	// prefix is component-wise, so a sibling sharing a name prefix does not
	// qualify — "/a/b" must not swallow "/a/bc".
	base := t.TempDir()
	beneath := filepath.Join(base, "pkg")
	require.NoError(t, os.MkdirAll(beneath, 0o755))

	ok, err := BelongsToTree(write(t, "a.jsonl", sessionRecord("u-1", "s", beneath)), base)
	require.NoError(t, err)
	assert.True(t, ok, "a subdirectory of the tree is still the tree")

	adjacent := base + "c"
	require.NoError(t, os.MkdirAll(adjacent, 0o755))
	ok, err = BelongsToTree(write(t, "b.jsonl", sessionRecord("u-1", "s", adjacent)), base)
	assert.False(t, ok, "a name-prefix sibling is a different tree")
	assert.ErrorIs(t, err, ErrWrongTree)
}

func TestBelongsToTree_AllowsWhatItCannotJudge(t *testing.T) {
	// No cwd on any record, and an empty cwd to compare against: nothing to
	// disagree with, so both are allowed through. Absence of evidence must not
	// turn every session into a refusal — what is caught is positive
	// disagreement.
	path := write(t, "s-1.jsonl",
		`{"type":"file-history-snapshot","messageId":"m-1","isSnapshotUpdate":false,"snapshot":{}}`,
		`{"type":"user","uuid":"origin","parentUuid":null}`)

	ok, err := BelongsToTree(path, "/whatever")
	require.NoError(t, err)
	assert.True(t, ok, "144 real transcripts carry no cwd at all")

	withCwd := write(t, "s-2.jsonl", sessionRecord("u-1", "s-2", "/elsewhere"))
	ok, err = BelongsToTree(withCwd, "")
	require.NoError(t, err)
	assert.True(t, ok, "no tree was asked about")

	ok, err = BelongsToTree(filepath.Join(t.TempDir(), "absent.jsonl"), "/w")
	require.NoError(t, err)
	assert.True(t, ok, "an unreadable file is the reader's error to report")
}

func TestBelongsToTree_StatedLimits(t *testing.T) {
	// The stated limits of the tree check, pinned so they stay limits rather
	// than quietly becoming something worse. Each is a case the check ACCEPTS,
	// and the comment on BelongsToTree says so. This narrows the collision; it
	// does not close it.
	base := t.TempDir()
	victim := filepath.Join(base, "proj", "pkg")
	sibling := filepath.Join(base, "proj-pkg")
	require.NoError(t, os.MkdirAll(victim, 0o755))
	require.NoError(t, os.MkdirAll(sibling, 0o755))

	// A sibling session that really did cd into the victim tree records that
	// tree, so nothing written down distinguishes it.
	both := write(t, "s-1.jsonl",
		sessionRecord("u-1", "s-1", sibling),
		sessionRecord("u-2", "s-1", victim))
	ok, err := BelongsToTree(both, victim)
	require.NoError(t, err)
	assert.True(t, ok, "the file genuinely records a turn run in this tree")

	// A record longer than maxRecordBytes stops the scan, and a mismatch behind
	// it is not seen. BelongsToSession has the same gap on the same line, so the
	// two do not cover for each other.
	huge := write(t, "s-2.jsonl",
		`{"type":"user","uuid":"u-1","cwd":"`+sibling+`","pad":"`+strings.Repeat("x", maxRecordBytes+1)+`"}`,
		sessionRecord("u-2", "s-2", sibling))
	ok, err = BelongsToTree(huge, victim)
	require.NoError(t, err)
	assert.True(t, ok, "the oversized line ends the scan; this is the stated gap")
}

func TestBelongsToSession_KeepsLookingPastADisagreeingRecord(t *testing.T) {
	// A disagreeing record is not a verdict. One real transcript in this corpus
	// opens with 303 records carrying an OLDER session's id and only reaches its
	// own on line 304 — a resumed conversation whose earlier records were copied
	// forward. Concluding from the first id present refuses that file, denying a
	// legitimate session its own record.
	path := write(t, "x.jsonl",
		sessionRecord("u-1", "older-session", "/w"),
		sessionRecord("u-2", "older-session", "/w"),
		sessionRecord("u-3", "x", "/w"))

	ok, err := BelongsToSession(path, "x")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestBelongsToSession_RefusesWhenNoRecordAgrees(t *testing.T) {
	// The scan keeps going, but it is still a check: a file where NO record
	// names this session is refused, and the diagnosis names the first
	// disagreeing id rather than the last.
	path := write(t, "x.jsonl",
		sessionRecord("u-1", "someone-else", "/w"),
		sessionRecord("u-2", "a-third-party", "/w"))

	ok, err := BelongsToSession(path, "x")
	assert.False(t, ok)
	assert.ErrorIs(t, err, ErrWrongSession)
	assert.Contains(t, err.Error(), "someone-else")
}
