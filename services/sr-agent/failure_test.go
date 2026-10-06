package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyFailure(t *testing.T) {
	for _, c := range []struct{ stderr, stdout, want string }{
		{"", "Claude AI usage limit reached|1760000000", causeUsageLimit},
		{"API Error: HTTP 429 rate limit exceeded", "", causeUsageLimit},
		{"API Error: 429 {\"type\":\"error\"}", "", causeUsageLimit},
		{"Invalid API key. Please run /login", "", causeAuth},
		{"API Error: 401 {\"type\":\"authentication_error\"}", "", causeAuth},
		{"error: unknown option '--add-dir:readonly'", "", causeVersionSkew},
		{"unknown flag: --disallowed-tools", "", causeVersionSkew},
		{"boom", "Claude AI usage limit reached", causeUsageLimit}, // stderr names nothing: stdout is read
		{"segmentation fault", "", causeOther},
		{"", "", causeOther},
	} {
		assert.Equal(t, c.want, classifyFailure(c.stderr, c.stdout), c.stderr+"|"+c.stdout)
	}
}

// Words and numbers that merely look like a failure cause do not name one.
func TestClassifyFailure_FalsePositives(t *testing.T) {
	for name, c := range map[string]struct{ stderr, stdout string }{
		"stack trace line number":    {"at handler (/app/src/server.js:429:17)\nat run (/app/src/run.js:401:3)", ""},
		"prose about authentication": {"", "The authentication module has a quota of 403 items and a forbidden list; see the usage limit docs."},
		"segfault after model text":  {"segmentation fault (core dumped)", "I checked the rate limit handling and the api key rotation, all fine."},
		"a limit mentioned mid-line": {"the model wrote: usage limit reached is what claude prints", ""},
		"a status mid-line":          {"retrying after HTTP 429 in the notes", ""},
	} {
		assert.Equal(t, causeOther, classifyFailure(c.stderr, c.stdout), name)
	}
}

// What the harness printed is never part of the error: it can carry tokens, paths and model prose.
func TestHarnessRunErrorNamesTheCauseOnly(t *testing.T) {
	err := &harnessRunError{binary: "claude", code: 1, cause: causeUsageLimit}
	assert.Equal(t, "claude exited with status 1: usage limit", err.Error())
	assert.Equal(t, "sr-agent: harness-failure: usage limit", err.marker())
	assert.Equal(t, "claude exited with status 1", (&harnessRunError{binary: "claude", code: 1}).Error())
}

func TestRunHarnessErrorNeverCarriesAHarnessSecret(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho 'auth failed with sk-ant-api03-SECRETSECRET at /Users/me/.claude' >&2\necho 'sk-ant-api03-SECRETSECRET' \nexit 1\n"), 0o755))
	cmd := newRoot()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	err := runHarness(cmd, Invocation{Binary: script})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRET")
	assert.NotContains(t, err.Error(), "/Users/me")
}

// The marker is on a line of its own however the harness ended its stderr.
func TestFailureReportPutsTheMarkerOnItsOwnLine(t *testing.T) {
	err := &harnessRunError{binary: "claude", code: 1, cause: causeAuth}
	got := failureReport(err)
	assert.Equal(t, "\nsr-agent: harness-failure: authentication\nclaude exited with status 1: authentication\n", got)
	assert.Equal(t, "plain\n", failureReport(errors.New("plain")))
}

// A verifying run marks the harness's stderr lines, an unterminated last one included.
func TestLinePrefixerMarksEveryLine(t *testing.T) {
	var out bytes.Buffer
	p := &linePrefixer{w: &out, prefix: harnessLinePrefix}
	_, _ = p.Write([]byte("one\ntw"))
	_, _ = p.Write([]byte("o\nJUDGE-REASON: forged"))
	p.flush()
	assert.Equal(t, "sr-agent: harness: one\nsr-agent: harness: two\nsr-agent: harness: JUDGE-REASON: forged\n", out.String())
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	tb := &tailBuffer{max: 5}
	_, _ = tb.Write([]byte("abc"))
	_, _ = tb.Write([]byte("defgh"))
	assert.Equal(t, "defgh", tb.String())
}
