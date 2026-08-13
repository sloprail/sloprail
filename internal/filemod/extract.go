package filemod

import (
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
)

// Extract implements module.Module.
//
// Before an action, this reads what the harness is about to do: a write tool
// names its path outright, a shell command has to be parsed for the places it
// redirects into. Both are predictions, and the second is best-effort — a
// script the parser cannot see inside writes files nothing predicted.
//
// After a cycle it does not predict. It compares the tree against where the
// session started and reports what is actually different, which is what catches
// everything the prediction missed.
func (m *Module) Extract(in module.Input) ([]event.Event, error) {
	switch in[module.InputPhase] {
	case module.PhasePost:
		return m.extractObserved(in)
	default:
		return m.extractPending(in)
	}
}

// extractPending reads a pending action for the files it would touch.
func (m *Module) extractPending(module.Input) ([]event.Event, error) {
	return nil, nil // TODO
}

// extractObserved compares the tree against the session's starting point.
func (m *Module) extractObserved(module.Input) ([]event.Event, error) {
	return nil, nil // TODO
}
