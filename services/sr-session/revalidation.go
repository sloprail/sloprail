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
// A nil store is the ordinary shape when the session cannot be identified — a
// payload naming no record at all, an unreadable transcript, no data directory.
// A payload merely missing transcript_path is NOT one of those cases: the
// record is located by the harness's own naming instead, which is what makes
// this reachable under a harness that omits the field. Every method is written
// to work on a nil store, and to answer "run the hook" while doing so. Losing
// the record must cost re-judging, never a rule that stops firing.
type revalidation struct {
	store sessionstate.Store
}

// openRevalidation resolves the session's store, or reports why it could not.
//
// The identity is passed IN rather than derived here, and that is the point: the
// caller has already resolved it once, for the environment it gives each hook,
// and a second stableID call would be a second derivation free to drift from the
// first. A hook reading `session state` must land in the store this opens, so
// the two must key on one answer rather than on two computed the same way today.
//
// The id it is given is the CONVERSATION's — resolved the way `session id`
// resolves it, not the id the harness currently reports, which re-forks
// mid-conversation and would open an empty database halfway through a session
// and re-judge everything already settled.
func openRevalidation(sessionID, cwd string) (*revalidation, error) {
	path, err := sessionDBPath(cwd, sessionID)
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

// Subject reports which file an event is about and what content a verdict
// about it would be keyed on, or false when there is no such content.
//
// A subject exists only where the fingerprint identifies WHAT THE ACTION WOULD
// LEAVE. That is the whole basis of the exemption: a stored pass says "this
// guardrail has permitted these exact bytes", so the bytes it is keyed on must
// be the ones the guardrail is being asked to permit. Key it on anything else
// and a pass licenses content nobody judged.
//
// Which kinds can answer follows from that:
//
//   - PreFileCreate carries the pending content on the event, because the file
//     is not there and that is the only place a hook could see it. The bytes
//     the write would leave are in hand, so this has a subject.
//
//   - PostFileCreate and PostFileUpdate are observations of a cycle that has
//     already settled. The file on disk IS the outcome, so reading it is reading
//     the result rather than predicting it.
//
//   - PreFileUpdate does NOT have one, and this is the correction of a real
//     refusal bypass. The kind is declared to carry a path and no content, so
//     the resulting bytes are not merely unread but unavailable — an Edit
//     supplies a patch, not a body, and nothing here can apply it. Reading the
//     file instead fingerprints the content the write would REPLACE, which does
//     not move between two successive offers of different payloads: write
//     "benign", let it pass, then offer "SECRET=hunter2" against the same
//     unchanged disk, and the second offer matches the first's stored pass and
//     is skipped without ever being judged. Answering false costs an update
//     being re-judged every cycle; answering with the disk's fingerprint costs
//     enforcement.
//
//   - PreFileDelete leaves no content, and PostFileDelete observes that there is
//     none. Neither has bytes a pass could be a licence for, and fingerprinting
//     the doomed file on the Pre side would key an exemption on exactly the
//     content the action exists to remove.
//
// The Post branch was written ahead of the kinds that feed it, when the file
// module's observed extraction was still a stub. That extraction has since
// landed — Extract dispatches PhasePost to extractObserved — and this branch is
// exercised end to end by the change-detection and identity suites
// (tests/e2e/session/013, 016, 017 and 020). The case that comment named as the
// one to check when it landed, a Post event about a file a later cycle has
// changed again, is T020_01's third cycle.
//
// Recorded as history rather than deleted because the note read as a live
// warning that nothing here was covered, and it outlived the condition it
// described by long enough to be quoted in an audit as a coverage gap.
//
// False means the hook runs and its verdict is not recorded. That is the safe
// direction in every case: work is repeated, never skipped.
func (r *revalidation) Subject(e event.Event, cwd string) (subject, bool) {
	if r == nil || r.store == nil {
		return subject{}, false
	}
	f, err := filemod.FromEvent(e)
	if err != nil {
		return subject{}, false
	}

	switch e.Kind {
	case filemod.KindPreCreate:
		// The pending bytes, which for a creation are exactly what would land.
		return subject{Path: f.Path, Fingerprint: fingerprint.Of([]byte(f.NewContent))}, true

	case filemod.KindPostCreate, filemod.KindPostUpdate:
		fp, err := fingerprint.OfFile(resolve(cwd, f.Path))
		if err != nil {
			// Nothing readable to fingerprint. Not an error worth reporting: the
			// file an observation names may have been removed again by the time
			// anything looks, and the honest answer is that this content has no
			// identity to compare.
			return subject{}, false
		}
		return subject{Path: f.Path, Fingerprint: fp}, true

	default:
		// PreFileUpdate, PreFileDelete, PostFileDelete, and any kind from another
		// module. None of them can name bytes the action leaves behind, so none
		// of them may license a skip.
		return subject{}, false
	}
}

// Skip reports whether a guardrail may be spared this file, and reports any
// error that stopped it from finding out.
//
// Both halves of the exemption are the store's to apply, and they are applied
// there rather than here so that no caller can get them half-right: a row is a
// licence only when the content still matches AND the verdict was a pass.
//
// The error is returned rather than folded into the false, so a caller can say
// so. Both are needed: silently answering false is correct enforcement but
// leaves a session re-judging everything with nobody told why, and an error
// that reached the decision would be worse — a session whose record cannot be
// read has lost the right to exempt anything, not gained it. So the answer is
// false on any error, AND the error travels.
func (r *revalidation) Skip(guardrail string, s subject) (bool, error) {
	if r == nil || r.store == nil {
		return false, nil
	}
	ok, err := r.store.Skippable(s.Path, guardrail, s.Fingerprint)
	if err != nil {
		return false, fmt.Errorf("sloprail: read verdict for %s: %w", s.Path, err)
	}
	return ok, nil
}

// Record stores what a guardrail concluded about this content.
//
// A refusal is written, not dropped — by this function. Whether the ENGINE
// retains it depends on the dispatcher calling this on the refusing path too,
// which is a separate claim pinned separately: see
// TestPreTool_RefusalIsRecordedNotDropped.
//
// Writing it is what makes the violation resurface on every subsequent cycle:
// the row fails the passing half of the exemption for as long as the content
// stays as it is, so the hook is asked again and again until the content
// changes or the hook permits it.
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
