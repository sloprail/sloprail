package main

import (
	"strings"

	"github.com/sloprail/sloprail/internal/event"
)

// preRefusals collects every refusal the gates give one tool call's file events,
// keyed by the file each is about, and renders them as the call's one deny.
//
// Why one deny names every file: a pre-tool hook answers once per call, and a
// call that changes several files is refused whole. Naming only the first
// refused file sends the agent back to fix that one, run again, and be refused
// for the next — as many turns as there are refused files — or, worse, to guess
// the call was refused for the file it does not know about. So each refused file
// is named, beside the reason it was refused for.
//
// The same reason given for several files (`sed -i` over three files, each an
// update the engine cannot compute) is said once, followed by every file it
// applies to, rather than repeated per file.
// sr:invariant gates/multi-file-call-refused-whole
type preRefusals struct {
	// multiFile is true when the call changes more than one file. Only then is a
	// refusal prefixed with the file it is about: a single-file call's refusal
	// names its file by being about the only one, and keeps the wording it had.
	multiFile bool
	workspace string
	entries   []preRefusal
	// refusedDisplay is the set of files already refused, keyed by their display
	// path (relative to the workspace) — what refused checks against, so a path
	// spelled two ways (absolute vs. relative) for the same file is one entry.
	refusedDisplay map[string]bool
}

// preRefusal is one distinct refusal reason and the files it was given for,
// in the order they were refused.
type preRefusal struct {
	reason string
	paths  []string
}

// newPreRefusals starts an empty collection for a call whose Pre events
// are events. It counts the distinct files the call changes, which decides
// whether each refusal must name its file.
func newPreRefusals(events []event.Event, workspace string) *preRefusals {
	files := map[string]bool{}
	for _, e := range events {
		if isPreFileEvent(e.Kind) {
			if p := eventPath(e); p != "" {
				files[displayPath(p, workspace)] = true
			}
		}
	}
	return &preRefusals{multiFile: len(files) > 1, workspace: workspace}
}

// add records a refusal about path ("" for one that is about the guard itself,
// such as a match that does not compile, rather than about any one file).
func (r *preRefusals) add(path, reason string) {
	if path != "" {
		path = displayPath(path, r.workspace)
		if r.refusedDisplay == nil {
			r.refusedDisplay = map[string]bool{}
		}
		r.refusedDisplay[path] = true
	}
	for i := range r.entries {
		if r.entries[i].reason == reason {
			if path != "" && !containsString(r.entries[i].paths, path) {
				r.entries[i].paths = append(r.entries[i].paths, path)
			}
			return
		}
	}
	entry := preRefusal{reason: reason}
	if path != "" {
		entry.paths = []string{path}
	}
	r.entries = append(r.entries, entry)
}

// refused reports whether path has already been refused by an earlier guard —
// the write is already prevented, so no further guard need be asked about it
// (T017_07's per-file "first refusal ends the matter", which #87 leaves intact;
// only the axis of asking about every OTHER file is new). "" (no path) never
// reads as refused: it is never a file this checks against.
func (r *preRefusals) refused(path string) bool {
	if path == "" || r.refusedDisplay == nil {
		return false
	}
	return r.refusedDisplay[displayPath(path, r.workspace)]
}

// render is the call's deny text, or "" when nothing refused.
//
// One refusal of a single-file call is its reason exactly as before. Anything
// more — several refusals, or any refusal of a call that changes several files —
// is a list, one line per distinct reason, each naming the files it refused.
func (r *preRefusals) render() string {
	if len(r.entries) == 0 {
		return ""
	}
	if len(r.entries) == 1 && !r.multiFile {
		return r.entries[0].reason
	}
	var refused []string
	lines := make([]string, 0, len(r.entries))
	for _, entry := range r.entries {
		line := entry.reason
		if r.multiFile && len(entry.paths) > 0 {
			line = strings.Join(entry.paths, ", ") + ": " + entry.reason
		}
		for _, p := range entry.paths {
			if !containsString(refused, p) {
				refused = append(refused, p)
			}
		}
		lines = append(lines, line)
	}
	head := "this call was refused before it ran"
	if r.multiFile && len(refused) > 0 {
		head += "; every file it would change was checked, and the file-guards refused " + strings.Join(refused, ", ")
	}
	return head + ":\n  - " + strings.Join(lines, "\n  - ")
}
