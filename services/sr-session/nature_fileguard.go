package main

import (
	"path/filepath"
	"strings"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/guardrail"
)

// This file holds what a file-guard's `match` is asked and the vocabulary shared
// with the gate dispatch. A file-guard is a rule bound to a FILE'S STATE, and it
// judges COMMITS: at Stop each rule is evaluated once over the changeset of
// commits it has not yet passed (changeset_eval.go), with commit-required
// refusing uncommitted work first (commit_required.go). It never acts before a
// write — refusing a write or a delete BEFORE it lands is a gate's job
// (runGatesForEvents on a PreFileWrite / PreFileDelete trigger).
//
// # How a file is matched (not an event trigger)
//
// A gate matches an event's fields; a file-guard matches a FILE. Its `match` is a
// FileMatchExpression over FileMatchScope — the file's own path, its status in
// the changeset, the `sr:` markers it carries and carried, the range's commit
// trailers, and `context[<name>]`. The scope is FLAT — NOT the event nested under
// `event` a gate reads. guardrail.CompileFileMatch is the same compiler the
// loader validated the match with, so a glob and a full expression behave
// identically here and at load.

// fileGuardResult is one file-guard's outcome on one file: the guard's name, how a
// refusal should attribute it, whether it refused, and the reason to relay.
//
// Attribution carries the plugin-aware name (bare for a project's guard, plus
// " from plugin X" for a shipped one), so a Stop refusal names where a guard the
// project never wrote lives — the same reason the old format attributes by Origin.
// Name stays for the diagnostics keyed on the bare folder name (the re-entry
// guard, the revalidation key).
type fileGuardResult struct {
	Name        string
	Attribution string
	Refused     bool
	Reason      string
	// Path is the file a Post refusal is about; "" otherwise.
	Path string
}

// eventPath is the file a file event is about, as the event spells it.
func eventPath(e event.Event) string {
	p, _ := e.Fields[filemod.FieldPath].(string)
	return p
}

// displayPath is path as the agent would name it: relative to the workspace when
// it lies inside it, as given otherwise.
func displayPath(path, workspace string) string {
	if workspace == "" || !filepath.IsAbs(path) {
		return path
	}
	if rel, err := filepath.Rel(workspace, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel
	}
	return path
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// fileGuardSelects reports whether a file-guard's compiled match selects a file
// event, evaluating the match against the FileMatchScope built from that event
// and the context[] map.
//
// The scope is FLAT (the spec's FileMatchScope): `path`, `markers`, `context` at
// the top level — NOT the event nested under `event` a gate reads. So this builds
// an event.Event whose Fields ARE the scope, and hands it to the compiled
// matcher, which reads those names directly (CompileFileMatch compiles against
// fileMatchScope() with no declared kind, so the env is exactly the fields
// passed, no fill-in). markers is the file's own markers in wire form. There is no
// context: a file-guard cannot see session state.
func fileGuardSelects(match *guardrail.Matcher, e event.Event) (bool, error) {
	return match.Match(fileMatchScopeEvent(e))
}

// fileMatchScopeEvent builds the FileMatchScope an event presents to a
// file-guard's match: the file's path and markers off the event, flat.
//
// markers comes from the event's NEW markers — on a Post event these are the
// settled file's markers (Scan of what is on disk). That is the "the markers the
// file carries" FileMatchScope names: exactly what the settled file holds. The wire form (a list of
// {kind,fqn,line} objects) is what filemod already puts on the event under
// `newMarkers`, reused rather than re-scanned. On a delete, which has no result,
// it is the markers the file carried — see fileMarkers.
func fileMatchScopeEvent(e event.Event) event.Event {
	fields := map[string]any{
		"path":       e.Fields[filemod.FieldPath],
		"markers":    fileMarkers(e),
		"oldMarkers": fileOldMarkers(e),
	}
	return event.Event{Kind: e.Kind, Fields: fields}
}

// fileMarkers returns the markers a file-guard's `markers` scope reads for an
// event, as the wire-form list expr's `any(markers, .kind == …)` quantifies over.
//
// would-be result's on a Pre. A create/update declares `newMarkers`.
//
// A delete declares no `newMarkers` — nothing remains to carry any — and only
// reaches a guard that opted into deletions (`deletions: include` / `only`).
// For that guard the file it is being asked about is the one being removed, so
// its `markers` are the markers that file CARRIED: `oldMarkers`. Without this a
// marker-scoped guard (`any(markers, .kind == "invariant")`) that includes
// deletions would never select a delete at all, whatever the lost file held.
// Chosen by KIND, not by the absence of `newMarkers`: an update whose result
// carries no markers must read as marker-less, not fall back to the ones it
// just removed.
//
// Either way the result is a list, never nil — the same "always a list"
// discipline the event keeps, so `any(markers, …)` evaluates to false rather
// than erroring on a missing field.
func fileMarkers(e event.Event) []any {
	field := filemod.FieldNewMarkers
	if declaration.IsFileDeleteKind(e.Kind) {
		field = filemod.FieldOldMarkers
	}
	if v, ok := e.Fields[field].([]any); ok {
		return v
	}
	if v, ok := e.Fields[filemod.FieldOldMarkers].([]any); ok {
		return v
	}
	return []any{}
}

// fileOldMarkers returns the markers the file carried before this change — the
// scope's `oldMarkers`: from disk on a Pre update or delete, from the session's
// baseline on a Post one, and none on a create, which nothing preceded. Always a
// list, never nil, like fileMarkers. It is what lets a marker-scoped guard see a
// write that REMOVES a marker: `markers` alone reads that write as a file with
// none, and the guard never selects it.
func fileOldMarkers(e event.Event) []any {
	if v, ok := e.Fields[filemod.FieldOldMarkers].([]any); ok {
		return v
	}
	return []any{}
}

// resultKnown reports whether a Pre write event's result was computable — the
// `resultKnown` field filemod carries on the two Pre kinds whose result can
// arrive either way (PreFileUpdate and, since the notebook-create fix,
// PreFileCreate). Absent or false means the engine could not compute the write's
// outcome (a command-derived update, or a notebook create whose cell source is
// not the document), which a gate that reads the content must treat as unverifiable and
// fail closed on. On a kind that does not carry the field, the value is absent and
// this returns false — which is why isUnderivablePreWrite gates on the kind
// first, so a delete (no result, no field) is not mistaken for an unknown one.
func resultKnown(e event.Event) bool {
	v, ok := e.Fields[filemod.FieldResultKnown].(bool)
	return ok && v
}

// isUnderivablePreWrite reports whether a Pre event is a create or update whose
// result the engine could NOT derive — the case a pre-write gate must fail
// closed on, because it cannot verify a file whose settled bytes are unknown (the
// dispatch uses it to quote what sr-file said about a change it could not compute).
//
// Gated on the kind so it fires ONLY where resultKnown is a meaningful signal: a
// create or an update. A delete carries no result and no `resultKnown` field, so
// it is never "underivable" in this sense — a PreFileDelete gate
// judges the bytes about to be lost, which are known. Both the create and the
// update case are covered (the create was the silently-lost one: a NotebookEdit
// fresh-.ipynb PreFileCreate marks resultKnown false, and checking only the
// update let it false-pass).
func isUnderivablePreWrite(e event.Event) bool {
	switch e.Kind {
	case declaration.KindPreFileCreate, declaration.KindPreFileUpdate:
		return !resultKnown(e)
	default:
		return false
	}
}

// isPreFileEvent reports whether a kind is a PRE file event a gate can fire on.
// A delete is included: a gate bound to PreFileDelete may refuse an unasked
// deletion.
func isPreFileEvent(kind string) bool {
	switch kind {
	case declaration.KindPreFileCreate, declaration.KindPreFileUpdate, declaration.KindPreFileDelete:
		return true
	}
	return false
}
