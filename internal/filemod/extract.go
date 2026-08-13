package filemod

import (
	"encoding/json"
	"os"
	"path/filepath"

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
	// arguments — a rule never sees it, and nothing in this module branches on
	// it. That is deliberate; extractPending's doc comment argues it.
	Tool() string
	// Arguments are the tool's own, as the harness gave them.
	Arguments() json.RawMessage
}

// extractPending reads a pending action for the files it would touch.
//
// It dispatches on the SHAPE of the arguments and never on Tool(). That
// omission looks like a bug and has been reported as one, so the argument for
// it lives here, where someone about to "fix" it will read it.
//
// The objection is fair on its face: any tool whose arguments carry a
// `file_path` produces a file event, so `Read` — which is read-only — yields a
// PreFileCreate. That is real. It is measured, not hypothetical, and
// TestExtractPending_ReadOnlyToolStillProducesAnEvent pins it.
//
// It is still the right trade, for two reasons that a name allowlist cannot
// give back.
//
// The first is drift. A name allowlist is a list of strings that must track a
// vocabulary this engine does not own and is not told about. Claude Code
// renamed `Task` to `Agent` in v2.1.63; a corpus survey of 8,458 transcripts
// found filemod never noticed, because it never asked. Every module that DID
// ask broke silently — and silently is the point: an allowlist that has fallen
// behind does not error, it stops producing events, and a guardrail that stops
// firing looks exactly like a guardrail that is satisfied. That is the precise
// failure this engine exists to prevent, and it is worse than the false
// positive above, because a spurious event is visible to whoever reads the rule
// and a missing one is visible to no one.
//
// The second is that the shape already does most of the filtering, which is
// easy to miss because the defect report overstated the blast radius. Of the
// read-only tools named as producing bogus events, only `Read` actually does.
// `Grep` carries `pattern`/`path`, `Glob` carries `pattern`, `WebFetch`
// carries `url` — none carries `file_path`, so none reaches an event at all.
// The residue is `Read`, and one over-reported tool is a smaller wrong than a
// vocabulary that goes stale without saying so.
//
// commandmod makes the identical choice for the identical reason, so this is
// the engine's rule rather than this module's habit.
//
// What would change this: `Read`'s event is not merely spurious, it is
// mislabelled — a PreFileCreate for a file that already exists on disk and is
// only being read. If that becomes a problem worth solving, solve it on shape
// too. A create whose arguments carry no `content` key at all is not a write,
// and that is a question about the arguments rather than about the name.
// Distinguishing an absent `content` from an empty one needs the raw JSON
// rather than the decoded struct, which is why it is not done here today.
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

	f := FileEvent{Path: w.FilePath}
	var kind string
	switch m.lookAt(w.FilePath) {
	case presenceAbsent:
		kind = KindPreCreate
		// The file does not exist yet, so a rule that wants to look at what
		// would be written has nowhere else to look.
		f.Content = w.Content
		f.Markers = Scan(w.Content)
	case presenceFile:
		kind = KindPreUpdate
		f.Markers = m.markersOnDisk(w.FilePath)
	case presenceNotAFile:
		// A directory, a device, a socket. No file write can land here — the
		// harness's own write will fail — so there is no file modification to
		// report and no event to emit. Emitting PreFileUpdate, as this did,
		// invented an existing file out of a stat that only said "something is
		// here", and put every PreFileUpdate rule to work on it.
		//
		// Silence is right rather than an error: a module that finds nothing it
		// owns says nothing, the same as the no-file_path case above. The write
		// still fails, and it fails as the harness's error about a real
		// filesystem condition rather than as a guardrail verdict.
		return nil, nil
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

// extractObserved turns the difference between the tree and the session's
// baseline into one event per file.
//
// One event per file rather than one carrying a list: a rule about files is
// written against a file, and a cycle that touched a hundred of them should
// dispatch a hundred events each matcher narrows, not hand every hook a
// hundred-entry array to filter itself.
//
// Each path is classified from two facts and no prediction — whether it was
// there at the baseline, which the difference's producer holds, and whether it
// is there now, which is a stat. A tool call's claim about what it did reaches
// none of it.
//
// A path the producer names but that was neither there before nor there now
// yields nothing; see classify. A payload this module cannot read yields
// nothing either, which is the same answer extractPending gives and for the
// same reason: a module reads what it recognises.
func (m *Module) extractObserved(in module.Input) ([]event.Event, error) {
	observed, ok := in[module.InputPayload].(Observed)
	if !ok {
		return nil, nil
	}

	paths := observed.Paths()
	events := make([]event.Event, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		kind, reportable := classify(
			observed.ExistedAtBaseline(path),
			m.lookAt(filepath.Join(observed.Root(), path)) == presenceFile,
		)
		if !reportable {
			continue
		}
		// No content on any of them, including the create. Unlike PreFileCreate
		// the file is on disk by now and a hook can read it there; and for the
		// delete there is nothing left to read at all. Carrying content on one
		// kind and not the others would make the delete the odd case a hook has
		// to special-case, which is exactly the shape the Post kinds are
		// declared flat to avoid.
		events = append(events, FileEvent{Path: path}.Event(kind))
	}
	if len(events) == 0 {
		return nil, nil
	}
	return events, nil
}
