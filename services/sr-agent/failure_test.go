package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyFailure(t *testing.T) {
	for tail, want := range map[string]string{
		"Claude AI usage limit reached|1760000000":   causeUsageLimit,
		"Error: 429 rate limit exceeded":             causeUsageLimit,
		"Invalid API key. Please run /login":         causeAuth,
		"error: unknown option '--add-dir:readonly'": causeVersionSkew,
		"segmentation fault":                         causeOther,
		"":                                           causeOther,
	} {
		assert.Equal(t, want, classifyFailure(tail), tail)
	}
}

func TestHarnessRunErrorNamesTheCauseAndItsMarker(t *testing.T) {
	err := &harnessRunError{binary: "claude", code: 1, cause: causeUsageLimit, detail: "limit reached"}
	assert.Equal(t, "claude exited with status 1: usage limit (limit reached)", err.Error())
	assert.Equal(t, "sr-agent: harness-failure: usage limit", err.marker())
	assert.Equal(t, "claude exited with status 1", (&harnessRunError{binary: "claude", code: 1}).Error())
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	tb := &tailBuffer{max: 5}
	_, _ = tb.Write([]byte("abc"))
	_, _ = tb.Write([]byte("defgh"))
	assert.Equal(t, "defgh", tb.String())
	assert.Equal(t, "last", lastLine("first\n\nlast\n\n"))
}
