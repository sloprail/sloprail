package filemod

import (
	"errors"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/harness"
)

// EffectPending is a Pending that MAY also state the file effects of the call
// outright, for a harness whose write tool cannot be read from its arguments in the
// canonical shapes (Codex's apply_patch names several files in one patch). The
// harness adapter derives them; this module classifies each against the tree the
// way it does a Write or an Edit.
type EffectPending interface {
	Pending
	FileEffects() []harness.FileEffect
}

// extractEffects turns reported file effects into Pre events.
//
// The tool's own claim is not taken as the file's state: whether a path is created
// or updated is decided by what is on disk NOW, exactly as for a Write tool, so an
// "add" over an existing file is an update (the patch overwrites it) and an
// "update" of a file that is not there produces nothing (the tool will refuse it, so
// no modification is about to happen — the same silence errWillNotApply gives an edit
// that cannot apply). A delete of a file that is not there likewise.
//
// A reported result that could not be derived keeps resultKnown false, which is what
// lets a gate on newContent fail closed instead of judging "".
func (m *Module) extractEffects(pending EffectPending, effects []harness.FileEffect) ([]event.Event, error) {
	var events []event.Event
	var problems []error
	seen := map[string]bool{}
	headLookups := 0
	for _, e := range effects {
		if e.Path == "" {
			continue
		}
		key := Reportable(e.Path, pending.Root())
		if seen[key] {
			continue
		}
		p, err := lookAt(e.Path, e.Path)
		if err != nil && p == unknown && e.Kind != harness.FileCreate {
			problems = append(problems, err)
			continue
		}
		f := FileEvent{Path: key}
		switch e.Kind {
		case harness.FileDelete:
			if p != presentFile {
				continue
			}
			f.OldContent, f.OldContentKnown = readContent(e.Path, MaxDeleteReadBytes)
			f.OldMarkers = Scan(f.OldContent)
			if hr, ok := pending.(HeadReader); ok && !f.OldContentKnown && headLookups < maxHeadMarkerLookups {
				headLookups++
				if head, ok := hr.HeadContent(key, MaxDeleteReadBytes); ok {
					f.OldMarkers = Scan(head)
				}
			}
			events = append(events, f.Event(KindPreDelete))
		case harness.FileCreate, harness.FileUpdate:
			switch p {
			case absent, unknown:
				if e.Kind == harness.FileUpdate {
					continue
				}
				f.NewContent, f.ResultKnown = e.NewContent, e.ResultKnown
				if f.ResultKnown {
					f.NewMarkers = Scan(e.NewContent)
				}
				events = append(events, f.Event(KindPreCreate))
			case presentFile:
				before := m.contentOnDisk(e.Path)
				f.OldContent, f.OldMarkers = before, Scan(before)
				f.NewContent, f.ResultKnown = e.NewContent, e.ResultKnown
				if f.ResultKnown {
					f.NewMarkers = Scan(e.NewContent)
				}
				events = append(events, f.Event(KindPreUpdate))
			default:
				continue
			}
		default:
			continue
		}
		seen[key] = true
	}
	return events, errors.Join(problems...)
}
