package transcript

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// EnvelopeAt is the whole-envelope fetch the judge-prepare piece calls with a
// citation's path and line. cite mints the location; this reads back what sits
// there. These tests pin the two halves that matter: the whole envelope IS
// returned uncut for an answer-grounded line (so a judge sees the question), and
// NOTHING is returned for a line that carries no answer envelope (a plain message,
// or an ordinary tool result) — the layering the reviewer asked for, from the
// fetch side.

// TestEnvelopeAtReturnsWholeAnswerEnvelope: the line an AskUserQuestion answer sits
// on yields the WHOLE tool_result envelope — question, answer and trailer — not the
// extracted answer. This is the whole point of the helper: cite's own search keeps
// only the answer, but a judge needs the question beside it.
func TestEnvelopeAtReturnsWholeAnswerEnvelope(t *testing.T) {
	p := newProject(t)
	envelope := `The user answered: "which approach for the auth rewrite?"="go with the second option please". Read the answers carefully.`
	path := p.write("a-session",
		userMsg("u1", "here is the task"), // line 1
		record("a1", "u1"),                // line 2
		// line 3: the answer envelope.
		`{"type":"user","uuid":"u2","parentUuid":"a1","isSidechain":false,"message":{"role":"user","content":[`+
			`{"type":"tool_result","tool_use_id":"t1","content":`+jsonQuote(envelope)+`}]}}`,
	)

	got, err := EnvelopeAt(path, 3)
	require.NoError(t, err)
	require.Equal(t, []string{envelope}, got, "the WHOLE envelope is returned, uncut")
	// The question — which cite's own search never lets a quote match — is present,
	// because a judge needs it to read the answer.
	assert.Contains(t, got[0], "which approach for the auth rewrite?",
		"the question is in the envelope so the judge sees what was asked")
	assert.Contains(t, got[0], "go with the second option please", "the answer is there too")
}

// TestEnvelopeAtReturnsWholeMultiQuestionEnvelope: a multi-question answer is
// returned whole — every question and every answer — which is the opposite of what
// cite's answerText keeps, and deliberately so: the judge-prepare consumer needs
// the sibling questions and answers to know which one "the second option" was.
func TestEnvelopeAtReturnsWholeMultiQuestionEnvelope(t *testing.T) {
	p := newProject(t)
	envelope := `The user answered: "which store?"="the dotdir one", "required or optional?"="make it required". Read the answers carefully.`
	path := p.write("a-session",
		userMsg("u1", "kick it off"), // line 1
		// line 2: a two-question envelope.
		`{"type":"user","uuid":"u2","parentUuid":"u1","isSidechain":false,"message":{"role":"user","content":[`+
			`{"type":"tool_result","tool_use_id":"t1","content":`+jsonQuote(envelope)+`}]}}`,
	)

	got, err := EnvelopeAt(path, 2)
	require.NoError(t, err)
	require.Equal(t, []string{envelope}, got, "the whole multi-question envelope is returned uncut")
	// Both the questions (which cite's answer-only view drops) and both answers.
	assert.Contains(t, got[0], "which store?")
	assert.Contains(t, got[0], "required or optional?")
	assert.Contains(t, got[0], "make it required")
}

// TestEnvelopeAtSeveralEnvelopesOnOneTurn: a user turn carrying several
// AskUserQuestion tool_result blocks yields every genuine answer envelope, in the
// order they sit on the entry — the reason the return is a list.
func TestEnvelopeAtSeveralEnvelopesOnOneTurn(t *testing.T) {
	p := newProject(t)
	first := `The user answered: "first question?"="first answer". Read the answers carefully.`
	second := `The user answered: "second question?"="second answer". Read the answers carefully.`
	path := p.write("a-session",
		userMsg("u1", "start"), // line 1
		// line 2: two tool_result blocks on ONE user turn.
		`{"type":"user","uuid":"u2","parentUuid":"u1","isSidechain":false,"message":{"role":"user","content":[`+
			`{"type":"tool_result","tool_use_id":"t1","content":`+jsonQuote(first)+`},`+
			`{"type":"tool_result","tool_use_id":"t2","content":`+jsonQuote(second)+`}]}}`,
	)

	got, err := EnvelopeAt(path, 2)
	require.NoError(t, err)
	require.Equal(t, []string{first, second}, got, "both envelopes, in the order they sit on the entry")
}

// TestEnvelopeAtPlainMessageYieldsNothing: a line naming a plain typed message has
// no envelope to fetch. This is the normal case for a message-grounded citation —
// (nil, nil), not an error: there is simply nothing extra for a judge to read.
func TestEnvelopeAtPlainMessageYieldsNothing(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session", userMsg("u1", "please refactor the auth module")) // line 1

	got, err := EnvelopeAt(path, 1)
	require.NoError(t, err, "a plain-message line is not an error, just nothing to fetch")
	assert.Empty(t, got, "a plain typed message carries no answer envelope")
}

// TestEnvelopeAtOrdinaryToolResultYieldsNothing: a line whose user turn carries an
// ordinary tool_result — a command's output that is not an answer envelope — yields
// nothing, the same answer/output line cite's own search draws. Its body is a
// tool's, not the user's, and is never handed to a judge as the user's words.
func TestEnvelopeAtOrdinaryToolResultYieldsNothing(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		// line 1: a user turn carrying a plain command result, no answer prefix.
		`{"type":"user","uuid":"u1","parentUuid":null,"isSidechain":false,"message":{"role":"user","content":[`+
			`{"type":"tool_result","tool_use_id":"t1","content":"total 42\n-rw-r--r-- 1 u staff file.go"}]}}`,
	)

	got, err := EnvelopeAt(path, 1)
	require.NoError(t, err)
	assert.Empty(t, got, "an ordinary tool result is not an answer envelope")
}

// TestEnvelopeAtLineWithNoEntryIsError: a line that names no entry — past the end of
// the file, or a preamble line Read skips — is ErrNoEntryAtLine, distinct from the
// empty-but-fine result above. A citation's line always names a real entry, so this
// is a caller holding a line that did not come from Cite, or a file that changed.
func TestEnvelopeAtLineWithNoEntryIsError(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		preamble(),          // line 1: no uuid — not an entry
		userMsg("u1", "hi"), // line 2: the only entry
	)

	// Past the end of the file.
	_, err := EnvelopeAt(path, 99)
	require.ErrorIs(t, err, ErrNoEntryAtLine, "a line past the end names no entry")

	// A preamble line that Read skips: counted, but not an entry.
	_, err = EnvelopeAt(path, 1)
	require.ErrorIs(t, err, ErrNoEntryAtLine, "a skipped preamble line names no entry")

	// And the real entry resolves — line 2, not line 1 — proving the line numbering
	// is physical (the preamble is counted) and the entry is found there.
	got, err := EnvelopeAt(path, 2)
	require.NoError(t, err)
	assert.Empty(t, got, "the plain message on line 2 has no envelope, but is a real entry")
}

// TestEnvelopeAtUnreadableFileErrors: a file that cannot be read propagates its read
// error, distinct from both ErrNoEntryAtLine and the empty result — the consumer can
// tell "the environment is broken" from "nothing to add for this citation".
func TestEnvelopeAtUnreadableFileErrors(t *testing.T) {
	p := newProject(t)
	missing := filepath.Join(p.dir, "does-not-exist.jsonl")

	_, err := EnvelopeAt(missing, 1)
	require.Error(t, err, "a missing transcript is a read error, not an empty result")
	assert.NotErrorIs(t, err, ErrNoEntryAtLine,
		"an unreadable file is a different failure than a line naming no entry")
}
