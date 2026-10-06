package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
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
//
// Every pattern holds at the START of a line, after at most an "error:" or "API Error:" label.
// stdout is model prose in a successful run, so it is read only when stderr names no cause.
const lead = `(?im)^\s*(?:(?:api )?error:?\s*)?`

var (
	usageLimitRe = regexp.MustCompile(lead + `(?:claude ai )?(?:usage limit (?:reached|exceeded)|rate limit (?:exceeded|reached)|you[’']ve hit your (?:usage )?limit|credit balance is too low)` +
		`|(?im)^\s*(?:(?:api )?error:?\s*)?(?:http\s*)?429\b`)
	authRe = regexp.MustCompile(lead + `(?:invalid (?:x-)?api[ -]key|please run /login|not logged in|failed to authenticate|oauth token has expired)` +
		`|(?im)^\s*(?:(?:api )?error:?\s*)?(?:http\s*)?40[13]\b`)
	skewRe = regexp.MustCompile(lead + `(?:unknown (?:flag|option|argument)|unrecognized (?:flag|option|argument))\b`)
)

// classifyFailure names the cause of a harness failure from what it printed: stderr first, and
// stdout when stderr names none (claude reports a usage limit on stdout).
func classifyFailure(stderr, stdout string) string {
	if c := classifyText(stderr); c != causeOther {
		return c
	}
	return classifyText(stdout)
}

func classifyText(text string) string {
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

// harnessLinePrefix marks a line of the harness's own stderr in a verifying run's stderr
// (services/sr-agent -> internal/dispatch): a reader of the verifier's answer skips such lines.
const harnessLinePrefix = "sr-agent: harness: "

// linePrefixer writes w line by line, each prefixed, holding an unterminated line until flush.
type linePrefixer struct {
	w      io.Writer
	prefix string
	mu     sync.Mutex
	part   []byte
}

func (l *linePrefixer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.part = append(l.part, p...)
	for {
		i := bytes.IndexByte(l.part, '\n')
		if i < 0 {
			break
		}
		fmt.Fprintf(l.w, "%s%s\n", l.prefix, l.part[:i])
		l.part = l.part[i+1:]
	}
	return len(p), nil
}

func (l *linePrefixer) flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.part) > 0 {
		fmt.Fprintf(l.w, "%s%s\n", l.prefix, l.part)
		l.part = nil
	}
}

// failureReport is what main prints for a failed run: the cause marker on a line of its own, even
// when the harness left its last line unterminated, then the error.
func failureReport(err error) string {
	var runErr *harnessRunError
	if errors.As(err, &runErr) && runErr.marker() != "" {
		return "\n" + runErr.marker() + "\n" + err.Error() + "\n"
	}
	return err.Error() + "\n"
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
