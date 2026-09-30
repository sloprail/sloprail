package main

import (
	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/guardrail"
)

// What a file-guard's `match` is asked about one file of a changeset, and how
// markers are read out of it. Shared by everything that builds a changeset, so
// `sr-session changeset` and a Stop evaluation select exactly the same files.

// changesetMarkers reads a file's markers in the changeset's own type. It is
// filemod's scan, unchanged: a marker in a commit is the same `sr:<kind> <fqn>`
// annotation it is on disk.
func changesetMarkers(text string) []changeset.Marker {
	found := filemod.Scan(text)
	out := make([]changeset.Marker, 0, len(found))
	for _, m := range found {
		out = append(out, changeset.Marker{Kind: m.Kind, FQN: m.FQN, Line: m.Line})
	}
	return out
}

// changesetSelector is a file-guard's `match` as a changeset.Options.Select.
//
// The scope is the file-guard match scope: `path`, `status`, `markers`,
// `oldMarkers`, `trailers` (the range's, key to values) and `context`. An
// evaluation error is returned, never read as "not selected": a match that could
// not decide has not decided the file is none of the rule's business.
func changesetSelector(match *guardrail.Matcher, context map[string]any) func(changeset.Scope) (bool, error) {
	return func(s changeset.Scope) (bool, error) {
		return match.Match(event.Event{Kind: changeset.Kind, Fields: map[string]any{
			"path":       s.Path,
			"status":     s.Status,
			"markers":    markersWire(s.Markers),
			"oldMarkers": markersWire(s.OldMarkers),
			"trailers":   trailersWire(s.Trailers),
			"context":    context,
		}})
	}
}

// markersWire is the list-of-{kind,fqn,line} form an expression quantifies over.
// Never nil, so `any(markers, …)` is false rather than an error on a file with none.
func markersWire(ms []changeset.Marker) []any {
	out := make([]any, 0, len(ms))
	for _, m := range ms {
		out = append(out, map[string]any{"kind": m.Kind, "fqn": m.FQN, "line": m.Line})
	}
	return out
}

func trailersWire(t map[string][]string) map[string]any {
	out := make(map[string]any, len(t))
	for k, values := range t {
		list := make([]any, 0, len(values))
		for _, v := range values {
			list = append(list, v)
		}
		out[k] = list
	}
	return out
}
