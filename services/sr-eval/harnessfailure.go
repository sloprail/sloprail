package main

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
)

// harnessFailureMarker is the line sr-agent prints on stderr when the harness it ran died
// (services/sr-agent failureMarker); what follows is the cause it read off the harness's own
// output: usage limit, authentication, version skew, other.
const harnessFailureMarker = "sr-agent: harness-failure:"

// failureWatch passes the agent's stderr through and remembers the cause on the first
// harness-failure line, across writes that split a line.
type failureWatch struct {
	mu    sync.Mutex
	part  []byte
	cause string
}

func (f *failureWatch) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.part = append(f.part, p...)
	for {
		i := bytes.IndexByte(f.part, '\n')
		if i < 0 {
			break
		}
		f.see(string(f.part[:i]))
		f.part = f.part[i+1:]
	}
	if len(f.part) > 4096 {
		f.part = f.part[len(f.part)-4096:] // a marker line is short; a runaway one is not it
	}
	return len(p), nil
}

func (f *failureWatch) see(line string) {
	line = strings.TrimSpace(line)
	if f.cause != "" || !strings.HasPrefix(line, harnessFailureMarker) {
		return
	}
	if f.cause = strings.TrimSpace(strings.TrimPrefix(line, harnessFailureMarker)); f.cause == "" {
		f.cause = "other"
	}
}

// Cause is the harness failure sr-agent named, "" when it named none.
func (f *failureWatch) Cause() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.part) > 0 {
		f.see(string(f.part))
		f.part = nil
	}
	return f.cause
}

// runOutcome is the line a run ends with and the exit status it carries.
//
// A run whose agent-under-test's harness died (a usage limit hit, a login refused) did not
// run the task: whatever the scorer says of the partial transcript is not a verdict on the
// agent, so it is exit 2, "the run itself could not be completed" — never PASS, and not an
// ordinary FAIL either, which would count against the guardrail or the model.
// sr:invariant authoring-tools/eval-unrunnable-is-not-a-failure
func runOutcome(sr scoreResult, scoreErr error, harnessFailures []string) (line string, code int) {
	switch {
	case len(harnessFailures) > 0:
		scored := "FAIL"
		switch {
		case scoreErr != nil:
			scored = "none, the scorer could not run"
		case sr.Passed:
			scored = "PASS"
		}
		return fmt.Sprintf("sr-eval: ERROR (the run could not be completed: the agent-under-test's harness failed — %s; the scorer's word on the partial run, %s, is not a verdict)",
			strings.Join(harnessFailures, "; "), scored), 2
	case scoreErr != nil:
		return fmt.Sprintf("sr-eval: FAIL (scorer could not run: %v)", scoreErr), 2
	case sr.Passed:
		return "sr-eval: PASS", 0
	}
	return "sr-eval: FAIL — " + sr.Reason, 1
}
