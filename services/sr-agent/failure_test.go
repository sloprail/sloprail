package main

import (
	"bytes"
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
		{"Invalid API key. Please run /login", "", causeAuth},
		{"error: unknown option '--add-dir:readonly'", "", causeVersionSkew},
		{"unknown flag: --disallowed-tools", "", causeVersionSkew},
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
		"stdout ignored with stderr": {"boom", "Claude AI usage limit reached"},
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
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho 'auth failed with sk-ant-api03-SECRETSECRETSECRET at /Users/me/.claude' >&2\necho 'sk-ant-api03-SECRETSECRETSECRET' \nexit 1\n"), 0o755))
	cmd := newRoot()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	err := runHarness(cmd, Invocation{Binary: script})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRET")
	assert.NotContains(t, err.Error(), "/Users/me")
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	tb := &tailBuffer{max: 5}
	_, _ = tb.Write([]byte("abc"))
	_, _ = tb.Write([]byte("defgh"))
	assert.Equal(t, "defgh", tb.String())
}
