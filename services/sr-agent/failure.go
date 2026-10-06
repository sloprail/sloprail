package main

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"sync"
)

// Why a harness died, read from what it printed. A non-zero status alone ("claude exited with
// status 1") cannot tell an exhausted usage limit from a bad login from a flag this build of the
// harness does not know, and a bad stretch of any of them used to read as N identical refusals.
const (
	causeUsageLimit  = "usage limit"
	causeAuth        = "authentication"
	causeVersionSkew = "version skew"
	causeOther       = "other"
)

// failureMarker prefixes the one stderr line sr-agent prints when its harness failed, so a caller
// (the judge runner) reads the cause without parsing the harness's own words.
const failureMarker = "sr-agent: harness-failure:"

// maxTail bounds how much of a harness's output is kept for the diagnosis.
const maxTail = 2048

// The patterns are the harnesses' own words for a failure, anchored: a bare status number or a
// word like "quota" or "authentication" also appears in stack traces, file paths and the model's
// prose, and would name the wrong cause.
var (
	usageLimitRe = regexp.MustCompile(`(?i)usage limit (reached|exceeded)|hit your (usage )?limit|rate limit (exceeded|reached)|rate_limit_error|overloaded_error|\bHTTP 429\b|credit balance is too low`)
	authRe       = regexp.MustCompile(`(?i)invalid (x-)?api[ -]key|please run /login|not logged in|authentication_error|\bHTTP 40[13]\b|failed to authenticate|oauth token has expired`)
	skewRe       = regexp.MustCompile(`(?im)^\s*(error:\s*)?(unknown (flag|option|argument)|unrecognized (flag|option|argument))\b`)
)

// classifyFailure names the cause of a harness failure from what it printed: stderr first, and
// stdout only when stderr said nothing (claude reports a usage limit on stdout).
func classifyFailure(stderr, stdout string) string {
	text := stderr
	if strings.TrimSpace(text) == "" {
		text = stdout
	}
	switch {
	case usageLimitRe.MatchString(text):
		return causeUsageLimit
	case authRe.MatchString(text):
		return causeAuth
	case skewRe.MatchString(text):
		return causeVersionSkew
	}
	return causeOther
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = t.buf[over:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(bytes.ToValidUTF8(t.buf, nil))
}

// teeTail writes to w and remembers the tail of what went through.
func teeTail(w io.Writer, tail *tailBuffer) io.Writer {
	if w == nil {
		return tail
	}
	return io.MultiWriter(w, tail)
}
