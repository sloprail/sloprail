package main

import (
	"fmt"
	"path/filepath"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/fingerprint"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// subject is the file one guardrail is about to be asked about, and what its
// content currently fingerprints to.
//
// The two travel together because neither is usable alone: a stored pass is a
// licence to skip only while the content it was given still yields the same
// fingerprint, and a fingerprint says nothing without the path it belongs to.
type subject struct {
	// Path as the event named it, which is what a verdict is recorded under.
	Path string
	// Fingerprint of the content a hook would judge.
	Fingerprint string
}

// revalidation decides which hooks a cycle can skip, and remembers what the
// ones that ran concluded.
//
// It holds a store rather than opening one per question: a hook point is one
// process handling one event, and re-opening the session database for every
// guardrail bound to it would pay the migration check each time.
//
// A nil store is the ordinary shape when the session cannot be identified —
// no transcript on the payload, no data directory. Every method is written to
// work on one, and to answer "run the hook" while doing so. Losing the record
// must cost re-judging, never a rule that stops firing.
type revalidation struct {
	store sessionstate.Store
}

// openRevalidation resolves the session's store, or reports why it could not.
//
// The session is the CONVERSATION, resolved the same way `session id` resolves
// it, not the id the harness currently reports — that one re-forks
// mid-conversation, and a store keyed on it would open an empty database
// halfway through a session and re-judge everything already settled.
func openRevalidation(p HookPayload) (*revalidation, error) {
	id, err := stableID(p)
	if err != nil {
		return nil, err
	}
	path, err := sessionDBPath(p.Cwd, id)
	if err != nil {
		return nil, err
	}
	store, err := sessionstate.Open(path)
	if err != nil {
		return nil, err
	}
	return &revalidation{store: store}, nil
}

// Close releases the database. Safe on a nil revalidation, so a caller that
// could not open one still defers this rather than branching around it.
func (r *revalidation) Close() {
	if r == nil || r.store == nil {
		return
	}
	r.store.Close()
}

// Subject reports which file an event is about and what it currently
// fingerprints to, or false when the event has no such file.
//
// Not every event has one. A command event names no file, and a file event
// about something that has since been removed has no content to fingerprint.
// Both answer false, which means the hook runs and its verdict is not
// recorded — there is nothing to key it on.
//
// Where the content is read from follows what the hook itself is given. A
// creation carries the pending content on the event — that is the only place a
// hook can see it, since the file is not there — so that is what is
// fingerprinted. Every other kind gives the hook a path and nothing else, so
// what the hook reads is the file, and the file is what is fingerprinted.
//
// Which means a pending change is judged, and recorded, against the content
// that is there when the hook runs rather than the content the write will
// leave. That is not a shortcut: it is the same content the hook itself looked
// at, so the verdict describes what was judged. Once the write lands the file
// no longer yields that fingerprint, and the next cycle judges it again.
func (r *revalidation) Subject(e event.Event, cwd string) (subject, bool) {
	if r == nil || r.store == nil {
		return subject{}, false
	}
	f, err := filemod.FromEvent(e)
	if err != nil {
		return subject{}, false
	}

	if e.Kind == filemod.KindPreCreate {
		return subject{Path: f.Path, Fingerprint: fingerprint.Of([]byte(f.Content))}, true
	}

	fp, err := fingerprint.OfFile(resolve(cwd, f.Path))
	if err != nil {
		// Nothing readable to fingerprint. Not an error worth reporting: a
		// delete event names a file that is meant to be gone, and the honest
		// answer either way is that this content has no identity to compare.
		return subject{}, false
	}
	return subject{Path: f.Path, Fingerprint: fp}, true
}

// Skip reports whether a guardrail may be spared this file.
//
// Both halves of the exemption are the store's to apply, and they are applied
// there rather than here so that no caller can get them half-right: a row is a
// licence only when the content still matches AND the verdict was a pass.
// Anything else runs the hook, and so does any error — a session whose record
// cannot be read has lost the right to exempt anything, not gained it.
func (r *revalidation) Skip(guardrail string, s subject) bool {
	if r == nil || r.store == nil {
		return false
	}
	ok, err := r.store.Skippable(s.Path, guardrail, s.Fingerprint)
	return err == nil && ok
}

// Record stores what a guardrail concluded about this content.
//
// A refusal is written, not dropped. That is what makes the violation resurface
// on every subsequent cycle: the row fails the passing half of the exemption
// for as long as the content stays as it is, so the hook is asked again and
// again until the content changes or the hook permits it.
func (r *revalidation) Record(guardrail string, s subject, passed bool) error {
	if r == nil || r.store == nil {
		return nil
	}
	err := r.store.RecordFileCheck(s.Path, guardrail, sessionstate.Verdict{
		Fingerprint: s.Fingerprint,
		Passed:      passed,
	})
	if err != nil {
		return fmt.Errorf("sloprail: record verdict for %s: %w", s.Path, err)
	}
	return nil
}

// resolve turns an event's path into one the filesystem will accept.
//
// Paths on file events are the project's own — relative to where the session
// runs. Joining is what makes the fingerprint read the file the hook would
// read, rather than whatever sits at the same relative path from the process's
// working directory. An absolute path is already an answer and is left alone.
func resolve(cwd, path string) string {
	if filepath.IsAbs(path) || cwd == "" {
		return path
	}
	return filepath.Join(cwd, path)
}
