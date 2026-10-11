package main

import (
	"errors"
	"strings"
	"testing"
)

// A run whose agent-under-test's harness died (a usage limit hit) was not completed: it is
// exit 2 and never says PASS, even when the scorer passes the partial transcript.
// sr:proves authoring-tools/eval-unrunnable-is-not-a-failure
func TestRunOutcome_AHarnessFailureIsNeverAPass(t *testing.T) {
	failed := []string{"turn 1: usage limit"}
	cases := []struct {
		name     string
		sr       scoreResult
		scoreErr error
		failures []string
		code     int
		says     string
	}{
		{"pass", scoreResult{Passed: true}, nil, nil, 0, "sr-eval: PASS"},
		{"fail", scoreResult{Reason: "no refusal"}, nil, nil, 1, "FAIL — no refusal"},
		{"scorer broke", scoreResult{}, errors.New("boom"), nil, 2, "scorer could not run"},
		{"harness died, scorer passed", scoreResult{Passed: true}, nil, failed, 2, "usage limit"},
		{"harness died, scorer failed", scoreResult{Reason: "x"}, nil, failed, 2, "usage limit"},
		{"harness died, scorer broke", scoreResult{}, errors.New("boom"), failed, 2, "usage limit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line, code := runOutcome(c.sr, c.scoreErr, c.failures)
			if code != c.code || !strings.Contains(line, c.says) {
				t.Errorf("got %d %q, want %d and a line saying %q", code, line, c.code, c.says)
			}
			if c.failures != nil && (strings.HasPrefix(line, "sr-eval: PASS") || !strings.HasPrefix(line, "sr-eval: ERROR")) {
				t.Errorf("a run whose harness died must end in ERROR, never PASS: %q", line)
			}
		})
	}
}

// sr-agent's harness-failure line is found in the agent's stderr however the writes split
// it, the first cause wins, and stderr without one names none.
func TestFailureWatch_ReadsSrAgentsMarker(t *testing.T) {
	var w failureWatch
	for _, chunk := range []string{"claude: something\n\nsr-agent: harness-fa", "ilure: usage limit\nclaude exited with status 1\n", "sr-agent: harness-failure: other\n"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.Cause(); got != "usage limit" {
		t.Errorf("cause %q, want usage limit", got)
	}

	var unterminated failureWatch
	_, _ = unterminated.Write([]byte("sr-agent: harness-failure: authentication"))
	if got := unterminated.Cause(); got != "authentication" {
		t.Errorf("an unterminated last line: cause %q", got)
	}

	var none failureWatch
	_, _ = none.Write([]byte("claude exited with status 1\nthe agent said harness-failure: in prose\n"))
	if got := none.Cause(); got != "" {
		t.Errorf("no marker line, but cause %q", got)
	}
}
