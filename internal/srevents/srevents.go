// Package srevents is the rule-decision log sr-test reads: when SR_EVENTS_FILE is set,
// every decision a rule makes (a gate, a file-guard, a structure gate, a context
// entering or leaving) appends one JSON line to it.
//
// The log is an observation, never an input: a failure to write it is warned about on
// stderr and never changes a verdict.
package srevents

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// EnvFile names the file the log is appended to.
const EnvFile = "SR_EVENTS_FILE"

// Event kinds.
const (
	GateChecked        = "GateChecked"
	FileGuardChecked   = "FileGuardChecked"
	StructureChecked   = "StructureChecked"
	ContextActivated   = "ContextActivated"
	ContextDeactivated = "ContextDeactivated"
)

// Outcomes.
const (
	Permitted = "permitted"
	Refused   = "refused"
	Passed    = "passed"
)

// Event is one line of the log.
type Event struct {
	Kind      string `json:"kind"`
	Rule      string `json:"rule"`
	Outcome   string `json:"outcome,omitempty"`
	FiredAt   string `json:"fired_at"`
	On        string `json:"on,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// Rule is how a refusal cites a rule: "<plugin>/<name>" for a plugin's, the bare name
// for the project's own.
func Rule(plugin, name string) string {
	if plugin == "" {
		return name
	}
	return plugin + "/" + name
}

// Emit appends e to $SR_EVENTS_FILE, when set. FiredAt is filled when empty.
func Emit(e Event) { EmitTo(os.Stderr, e) }

// EmitTo is Emit with the warning writer given.
func EmitTo(warn io.Writer, e Event) {
	path := os.Getenv(EnvFile)
	if path == "" {
		return
	}
	if e.FiredAt == "" {
		e.FiredAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	b, err := json.Marshal(e)
	if err == nil {
		// One write per line on an O_APPEND handle: concurrent hook processes do not interleave.
		var f *os.File
		if f, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_, err = f.Write(append(b, '\n'))
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
	}
	if err != nil && warn != nil {
		fmt.Fprintf(warn, "sloprail: could not write %s: %v\n", EnvFile, err)
	}
}
