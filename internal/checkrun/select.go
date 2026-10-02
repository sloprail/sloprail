package checkrun

import (
	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/transcript"
)

// What a file-guard's `match` is asked about one file of a changeset, and how
// markers are read out of it. Shared by everything that builds a changeset, so
// `sr-session changeset` and a Stop evaluation select exactly the same files.

// Markers reads a file's markers in the changeset's own type. It is
// filemod's scan, unchanged: a marker in a commit is the same `sr:<kind> <fqn>`
// annotation it is on disk.
func Markers(text string) []changeset.Marker {
	found := filemod.Scan(text)
	out := make([]changeset.Marker, 0, len(found))
	for _, m := range found {
		out = append(out, changeset.Marker{Kind: m.Kind, FQN: m.FQN, Line: m.Line})
	}
	return out
}

// Selector is a file-guard's `match` as a changeset.Options.Select.
//
// The scope is the file-guard match scope: `path`, `status`, `markers`,
// `oldMarkers`, `trailers` (the range's, key to values) and `context`. An
// evaluation error is returned, never read as "not selected": a match that could
// not decide has not decided the file is none of the rule's business.
func Selector(match *guardrail.Matcher, context map[string]any) func(changeset.Scope) (bool, error) {
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

// ResolveCitations grounds a changeset's Sloprail-Cites-* trailers in the
// transcripts on disk, exactly as `sr-file --cite` would, and says which of the
// selected files each citation's commits changed. It returns the trailers that did
// not resolve. With no record there is nothing to resolve against, and no
// citations: a `require: citation` then refuses, as it must.
func ResolveCitations(cs *changeset.Changeset, record, cwd string) []changeset.Unresolved {
	if record == "" {
		return nil
	}
	project := sessionpath.ProjectDirOf(record, cwd)
	resolve := func(req transcript.CitationRequest) (transcript.Citation, error) {
		return transcript.ResolveCitationAcrossSessions(record, project, req)
	}
	cites, missed := changeset.ResolveCitations(cs.Commits, resolve)
	changeset.AttributeFiles(cites, cs.Files)
	cs.Citations = cites
	return missed
}

// TrustTrailers is ResolveCitations for a run with no session: each Sloprail-Cites-* trailer
// of the range counts as a citation in its own pool, unverified. It is what `verify` uses —
// the quote was resolved against the transcript where the author ran `run`, and the quote is
// part of the fingerprint, so a judge's verdict for it is found without the transcript.
func TrustTrailers(cs *changeset.Changeset) {
	cites, _ := changeset.ResolveCitations(cs.Commits, func(req transcript.CitationRequest) (transcript.Citation, error) {
		return transcript.Citation{Quote: req.Quote, SourceTypes: req.SourceTypes, Path: "(commit trailer)", Line: 1}, nil
	})
	changeset.AttributeFiles(cites, cs.Files)
	cs.Citations = cites
}
