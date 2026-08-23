package commandmod

import (
	"encoding/json"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
)

// Pending is what a module is given about an action a harness is about to
// take. A module reads what it recognises and ignores the rest.
//
// Declared here rather than imported from the file module: two modules reading
// the same payload is not two modules sharing a type, and making one depend on
// the other so they can agree on two method names would couple them for
// nothing.
type Pending interface {
	// Tool is what the harness calls it. Read only to know how to read the
	// arguments — a rule never sees it.
	Tool() string
	// Arguments are the tool's own, as the harness gave them.
	Arguments() json.RawMessage
}

// pendingCommand is the shape a shell tool's arguments take. Only the command
// is read; what else a harness puts there is its business.
type pendingCommand struct {
	Command string `json:"command"`
}

// Extract implements module.Module.
//
// Only the pre phase produces anything. A command that already ran is not
// something this module can report on — what it changed shows up as the file
// module's Post events, established by diff rather than inferred from a string
// that was never executed by us.
//
// Gated on HarnessCommandTools first, by tool name. This module used to
// dispatch on argument shape alone — any tool carrying a `command` key
// produced an event, and the doc comment here argued explicitly against
// naming the tool for the same drift reason filemod's extractPending once
// gave for itself: a harness renaming its shell tool, or adding a second
// one, should not silently stop being watched. That reasoning is preserved
// in git history rather than restated; see harnesstools.go for why this
// project chose a named allowlist anyway, and the maintenance obligation
// that choice carries.
func (m *Module) Extract(in module.Input) ([]event.Event, error) {
	if in[module.InputPhase] == module.PhasePost {
		return nil, nil
	}

	pending, ok := in[module.InputPayload].(Pending)
	if !ok {
		return nil, nil
	}

	if !HarnessCommandTools[pending.Tool()] {
		// Not a tool this project has named as running a shell command. No
		// shape fallback — see harnesstools.go: the tool-name gate is the
		// sole signal, deliberately, so a call whose arguments merely
		// happen to carry a `command` key under some other tool's name is
		// not treated as one.
		return nil, nil
	}

	var pc pendingCommand
	if err := json.Unmarshal(pending.Arguments(), &pc); err != nil || pc.Command == "" {
		// A recognised command tool whose arguments carry no command line —
		// ordinary, not an error.
		return nil, nil
	}

	return []event.Event{ExtractCommand(pc.Command).Event()}, nil
}

// ExtractCommand finds every invocation a command line performs.
//
// Exported because it is the whole of this module's logic and it is a pure
// function of a string — which is what lets it be tested by handing it a
// command line and reading what comes back, with no harness and no payload in
// the way.
//
// It never returns an error. A line that will not parse, or one whose programs
// cannot be resolved, still produces an event: the raw text is what a rule
// about unparseable commands would match on, and swallowing the event would
// mean the one command line nobody could read is also the one nobody is
// watching.
func ExtractCommand(raw string) CommandEvent {
	return CommandEvent{Raw: raw, Invocations: walk(raw)}
}
