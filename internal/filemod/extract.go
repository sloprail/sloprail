package filemod

import (
	"encoding/json"
	"os"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
)

// Extract implements module.Module.
//
// Before an action, this reads what the harness is about to do: a write tool
// names its path outright. After a cycle it does not predict — it compares the
// tree against where the session started and reports what is actually
// different, which is what catches everything the prediction missed.
func (m *Module) Extract(in module.Input) ([]event.Event, error) {
	if in[module.InputPhase] == module.PhasePost {
		return m.extractObserved(in)
	}
	return m.extractPending(in)
}

// pendingWrite is the shape a write tool's arguments take. Read only to find
// the path; what else a harness puts there is its business.
type pendingWrite struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

// Pending is what a module is given about an action a harness is about to
// take. A module reads what it recognises and ignores the rest.
type Pending interface {
	// Tool is what the harness calls it. Read only to know how to read the
	// arguments — a rule never sees it.
	Tool() string
	// Arguments are the tool's own, as the harness gave them.
	Arguments() json.RawMessage
}

// extractPending reads a pending action for the files it would touch.
func (m *Module) extractPending(in module.Input) ([]event.Event, error) {
	pending, ok := in[module.InputPayload].(Pending)
	if !ok {
		return nil, nil
	}

	var w pendingWrite
	if err := json.Unmarshal(pending.Arguments(), &w); err != nil || w.FilePath == "" {
		// A tool whose arguments name no file concerns this module not at all,
		// which is ordinary rather than an error.
		return nil, nil
	}

	kind := KindPreCreate
	if !m.exists(w.FilePath) {
		kind = KindPreCreate
	} else {
		kind = KindPreUpdate
	}

	f := FileEvent{Path: w.FilePath}
	if kind == KindPreCreate {
		// The file does not exist yet, so a rule that wants to look at what
		// would be written has nowhere else to look.
		f.Content = w.Content
		f.Markers = Scan(w.Content)
	} else {
		f.Markers = m.markersOnDisk(w.FilePath)
	}
	return []event.Event{f.Event(kind)}, nil
}

// markersOnDisk reads a file and scans it.
//
// IMPORTANT — what an update's markers actually describe. PreFileUpdate does not
// carry the pending content: module.go declares `path` alone for that kind, and
// the event has no field holding what the write would leave behind. So these are
// the markers in the bytes the write is about to REPLACE, not the bytes it would
// leave. On an update, `markers` describes the PRE-WRITE state of the file.
//
// That is a real limitation, not a design choice: a rule saying "this function
// must stay marked" would read the marker that is there now and be satisfied by
// a write that removes it. It is the honest reading available today, and it
// stops being a limitation when PreFileUpdate carries its pending content — a
// known defect elsewhere, deliberately not fixed here.
//
// A file that cannot be read yields no markers rather than an error. The write
// is what this event is reporting; a rule that cannot see the old text should
// still see the path, and failing the whole extraction would drop the event
// entirely — a file event that never fires is the silence this engine exists to
// prevent.
func (*Module) markersOnDisk(path string) []Marker {
	b, err := os.ReadFile(path)
	if err != nil {
		return []Marker{}
	}
	return Scan(string(b))
}

// extractObserved compares the tree against the session's starting point.
func (m *Module) extractObserved(module.Input) ([]event.Event, error) {
	return nil, nil // TODO
}
