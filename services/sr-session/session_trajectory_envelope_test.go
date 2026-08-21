package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The `trajectory envelope` command wraps internal/transcript EnvelopeAt (whose
// own extraction is unit-tested there). These pin the COMMAND's own contract: the
// required, positive --line; an --path trajectory read to the whole envelope at a
// line; and the empty-but-fine outcome for a line carrying no answer envelope,
// which prints nothing and exits 0 rather than erroring. This is the fetch a
// guardrail's judge-prepare calls after cite hands it a <path>:<line>.

// runEnvelope drives the real `trajectory envelope` command with the given args
// and returns its stdout, stderr and error — the command controls its own output
// through cobra, so the buffers capture exactly what a caller's shell would see.
func runEnvelope(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newSessionTrajectoryEnvelopeCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	// --path is always given here (these are agent-facing reads of a concrete
	// file), so resolveTrajectory never reaches for a hook payload on stdin.
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// writeEnvelopeTranscript writes a minimal JSONL transcript whose given line
// carries an AskUserQuestion answer envelope, and returns its path. The lines are
// exactly what transcript.ReadLines counts, so the returned line number is the one
// a cite citation would carry.
func writeEnvelopeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	return path
}

// --line is REQUIRED — without the line there is no entry to read back.
func TestEnvelopeCmd_LineIsRequired(t *testing.T) {
	path := writeEnvelopeTranscript(t, `{"type":"user","uuid":"u1","message":{"role":"user","content":"hi"}}`)
	_, _, err := runEnvelope(t, "--path", path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--line is required")
}

// A non-positive or non-numeric --line is refused — a physical line is 1-based.
func TestEnvelopeCmd_LineMustBePositiveInteger(t *testing.T) {
	path := writeEnvelopeTranscript(t, `{"type":"user","uuid":"u1","message":{"role":"user","content":"hi"}}`)
	for _, bad := range []string{"0", "-1", "notanumber", "1.5"} {
		_, _, err := runEnvelope(t, "--path", path, "--line", bad)
		require.Error(t, err, "line %q must be refused", bad)
		assert.Contains(t, err.Error(), "positive integer")
	}
}

// The whole envelope at the line — question and answer — is printed. This is the
// point of the command: cite's location gives the line, this reads back what sits
// there, question included.
func TestEnvelopeCmd_PrintsTheWholeEnvelopeAtTheLine(t *testing.T) {
	envelope := `The user answered: "which approach for the auth rewrite?"="go with the second option". Read the answers carefully.`
	path := writeEnvelopeTranscript(t,
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"here is the task"}}`,                                        // line 1
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}`, // line 2
		// line 3: the answer envelope.
		`{"type":"user","uuid":"u2","parentUuid":"a1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":`+jsonStringLit(envelope)+`}]}}`,
	)

	stdout, _, err := runEnvelope(t, "--path", path, "--line", "3")
	require.NoError(t, err)
	assert.Contains(t, stdout, "which approach for the auth rewrite?",
		"the QUESTION must be in the output — it is the whole reason to fetch the envelope")
	assert.Contains(t, stdout, "go with the second option", "the answer is there too")
}

// A line that carries no answer envelope — a plain typed message — prints NOTHING
// and exits 0. That is the message-grounded citation's normal case, not an error:
// there is simply no envelope to add.
func TestEnvelopeCmd_NoEnvelopeAtLineIsEmptyAndFine(t *testing.T) {
	path := writeEnvelopeTranscript(t,
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"please remove the second line"}}`, // line 1: plain message
	)
	stdout, _, err := runEnvelope(t, "--path", path, "--line", "1")
	require.NoError(t, err, "a message-grounded line is not an error")
	assert.Empty(t, strings.TrimSpace(stdout), "no envelope to print for a plain message")
}

// A line that names NO entry is an error (distinct from the empty-but-fine case),
// so a caller tells "points nowhere" from "nothing to add here".
func TestEnvelopeCmd_LinePointingNowhereIsAnError(t *testing.T) {
	path := writeEnvelopeTranscript(t,
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"hi"}}`, // only line 1 exists
	)
	_, _, err := runEnvelope(t, "--path", path, "--line", "99")
	require.Error(t, err, "a line past the end names no entry and must error")
}

// jsonStringLit renders s as a JSON string literal (quotes + escapes), for
// embedding an envelope's own text as the content value of a tool_result.
func jsonStringLit(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
