package transcript

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Searching the conversation's other transcripts — the half of the identity
// walk that has to look outside the file it was handed. Kept apart from the
// walk itself because it answers a different question: the walk asks "where did
// this conversation begin", this asks "which file holds this record".

// predecessors returns, in name order, the transcripts in the walk's project
// directory that the continuation opening with root may continue from.
//
// A predecessor is a transcript that holds the record root continues from
// (its logicalParentUuid) OR root itself, and that opens on a DIFFERENT, not
// later root. Each of those three conditions is there because of a shape real
// transcripts have, and each was the cause of real hooks running with no
// session identity at all:
//
//   - A different root. Compaction copies records verbatim into the file it
//     opens, and a resumed session re-forks that file, so the record a boundary
//     points at is usually in the file doing the pointing AND in every fork of
//     it — all of which open on the very same boundary record, uuid and all.
//     Measured: one conversation compacted once and was resumed into eight
//     files, every one opening on the same boundary and every one holding its
//     logical parent. Matching by "any other file holding the record" sent the
//     walk from one fork to the next until it came back round — "the chain
//     revisits", 110 failed hook runs across four sessions of it. A sibling is
//     never what a continuation continues; skipping every file opening on a
//     root already walked through is what makes each hop go backwards.
//   - Root itself, not only its logical parent. Compaction APPENDS its boundary
//     to the file it happened in and only a resume moves on to a new file, so
//     the predecessor is the file holding the boundary part-way down. That is
//     also the only trace left when the logical parent was never written:
//     measured, a preserved-segment compaction named a parent that no
//     transcript on the machine held as a record, while the file it compacted
//     held the boundary itself on line 3365 — 115 failed hook runs across five
//     forks, "no transcript holds the record".
//   - Not a later root. A later continuation can carry a verbatim copy of an
//     earlier one's records in its preserved tail, and walking INTO it would
//     walk forwards. Roots carry timestamps, and a predecessor began before
//     the continuation did. Where either timestamp will not parse the check is
//     skipped rather than guessed at.
//
// Name order is what makes every fork of a continuation — which all see the
// same candidates — land on the same predecessor, so they converge; the walk
// (walk.from) backtracks to the next candidate when one dead-ends.
//
// A file that holds no parentless record at all is not a transcript anyone
// continues from and is skipped, as is one that vanished between the listing
// and the read. A file that IS on disk but cannot be read to a decision (a
// record past maxRecordBytes, a permission error) is skipped too, rather than
// aborting the whole search — one bad file elsewhere in the project directory
// must never stop the walk from finding a different, readable predecessor. It
// is not treated as "gone" either: dropping it silently would key the session
// on a fallback that flips back to the origin the day the file reads again, so
// its path is remembered (walk.noteUnreadable) and reported once the search is
// over — by ResolveStableSessionID, or by the SessionStart notice when it is
// the reason the walk degraded (see noteDegradedIdentity).
func (w *walk) predecessors(root Entry) ([]candidate, error) {
	all, err := w.transcripts()
	if err != nil {
		return nil, err
	}
	var out []candidate
	for _, c := range all {
		if c.root.UUID == root.UUID || startedAfter(c.root, root) {
			continue
		}
		holds, err := containsAnyUUID(c.path, root.LogicalParentUUID, root.UUID)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			w.noteUnreadable(c.path)
			continue
		}
		if holds {
			out = append(out, c)
		}
	}
	return out, nil
}

// transcripts lists the project directory's transcripts with their roots,
// once per walk. A file that cannot be read to a decision is skipped and
// remembered (walk.noteUnreadable) rather than aborting the listing — see
// predecessors.
func (w *walk) transcripts() ([]candidate, error) {
	if w.listed {
		return w.listing, nil
	}
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", w.dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(w.dir, e.Name())
		root, err := rootRecord(path)
		if errors.Is(err, ErrNoOriginRecord) || errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			w.noteUnreadable(path)
			continue
		}
		w.listing = append(w.listing, candidate{path: path, root: root})
	}
	w.listed = true
	return w.listing, nil
}

// startedAfter reports whether cand's root was written after root was — that
// is, whether cand is a LATER continuation rather than an earlier one. False
// when either timestamp is missing or will not parse: the check narrows the
// candidates and must not invent a reason to discard one.
func startedAfter(cand, root Entry) bool {
	c, err := time.Parse(time.RFC3339Nano, cand.Timestamp)
	if err != nil {
		return false
	}
	r, err := time.Parse(time.RFC3339Nano, root.Timestamp)
	if err != nil {
		return false
	}
	return c.After(r)
}

// sameFile reports whether two paths name the same transcript. Compared after
// cleaning, so a path assembled differently from the one we were handed still
// counts as the file being left — the exclusion above is load-bearing, and a
// spelling difference must not defeat it.
func sameFile(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// containsAnyUUID reports whether any record in path has one of targets as its
// own uuid — any record, not just the root: a logical parent points wherever
// the earlier conversation had got to, which is somewhere in the middle of it.
// A file that cannot be read to the end is an error, never a "no": see
// predecessors.
func containsAnyUUID(path string, targets ...string) (bool, error) {
	found := false
	err := scanFile(path, func(rec claudeRecord) bool {
		for _, t := range targets {
			if t != "" && rec.UUID == t {
				found = true
				return false
			}
		}
		return true
	})
	if found {
		return true, nil
	}
	return false, err
}
