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

var (
	usageLimitRe = regexp.MustCompile(`(?i)usage limit|rate limit|rate_limit|quota|limit reached|too many requests|\b429\b|credit balance|overloaded|resource exhausted`)
	authRe       = regexp.MustCompile(`(?i)not logged in|please run /login|authentication|unauthorized|\b401\b|invalid api key|invalid x-api-key|api key|login required|forbidden|\b403\b`)
	skewRe       = regexp.MustCompile(`(?i)unknown (flag|option|argument|command)|unrecognized (flag|option|argument)|no such option|invalid (flag|option)|requires a newer|unsupported version`)
)

// classifyFailure names the cause of a harness failure from the tail of its output.
func classifyFailure(tail string) string {
	switch {
	case usageLimitRe.MatchString(tail):
		return causeUsageLimit
	case authRe.MatchString(tail):
		return causeAuth
	case skewRe.MatchString(tail):
		return causeVersionSkew
	}
	return causeOther
}

// lastLine is the last non-empty line of a tail, bounded: the harness's own words for the failure.
func lastLine(tail string) string {
	lines := strings.Split(strings.TrimSpace(tail), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			if len(l) > 240 {
				l = l[:240] + "..."
			}
			return l
		}
	}
	return ""
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
