package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Searching the conversation's other transcripts — the half of the identity
// walk that has to look outside the file it was handed. Kept apart from the
// walk itself because it answers a different question: the walk asks "where did
// this conversation begin", this asks "which file holds this record".

// findPredecessor returns the transcript in projectDir that the continuation
// opening with root continues from.
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
// Files are tried in name order, so every fork of a continuation — which all
// see the same candidates — lands on the same predecessor, and they converge.
//
// An unreadable sibling is skipped rather than fatal — another session's
// half-written file is not this conversation's problem. Finding nothing IS
// reported: ErrContinuationMissing when no file holds either record (the
// predecessor was deleted — Claude Code removes old transcripts on a cleanup
// period), ErrChainRunaway when the only files that do are ones the walk has
// already crossed.
func findPredecessor(projectDir string, root Entry, seen map[string]bool) (string, error) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", projectDir, err)
	}
	looped := ""
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(projectDir, e.Name())
		cand, err := rootRecord(path)
		if err != nil || cand.UUID == root.UUID || startedAfter(cand, root) {
			continue
		}
		if !containsAnyUUID(path, root.LogicalParentUUID, root.UUID) {
			continue
		}
		if seen[cand.UUID] {
			looped = path
			continue
		}
		return path, nil
	}
	if looped != "" {
		return "", fmt.Errorf("%w: the only transcript holding what %s continues, %s, is one the walk already crossed",
			ErrChainRunaway, root.UUID, looped)
	}
	return "", fmt.Errorf("%w: no transcript in %s opening on another root holds the record %s or %s itself",
		ErrContinuationMissing, projectDir, root.LogicalParentUUID, root.UUID)
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
//
// An unreadable file answers false rather than erroring; see findPredecessor
// on why a sibling is not fatal.
func containsAnyUUID(path string, targets ...string) bool {
	found := false
	_ = scanFile(path, func(rec claudeRecord) bool {
		for _, t := range targets {
			if t != "" && rec.UUID == t {
				found = true
				return false
			}
		}
		return true
	})
	return found
}
