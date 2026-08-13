package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Searching the conversation's other transcripts — the half of the identity
// walk that has to look outside the file it was handed. Kept apart from the
// walk itself because it answers a different question: the walk asks "where did
// this conversation begin", this asks "which file holds this record".

// findTranscriptContaining returns the transcript in projectDir holding a record
// whose own uuid is target, skipping leaving.
//
// leaving is always the file whose logical parent is being resolved, and
// skipping it is not tidiness — it is the difference between crossing a restart
// and going round in a circle. Compaction copies earlier records verbatim into
// the new file, uuids included, so the record a boundary points at is very
// often also present in the file doing the pointing.
//
// This is measured, not theorised. Across the 8,084 real transcripts in one
// ~/.claude, 34 open with a parentless record carrying a logicalParentUuid —
// and in 33 of those the record it names is ALSO in that same file, up to 756
// lines further down. So the self-match is not a rare edge: it is what almost
// every restart looks like. Matching there sends the walk straight back to the
// root it just read.
//
// An unreadable sibling is skipped rather than fatal — another session's
// half-written file is not this conversation's problem. Finding nothing at all
// IS fatal: a logical parent naming a record in no transcript means the
// environment lost a file, and answering anyway would answer wrongly.
func findTranscriptContaining(projectDir, target, leaving string) (string, error) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", projectDir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(projectDir, e.Name())
		if sameFile(path, leaving) {
			continue
		}
		if containsUUID(path, target) {
			return path, nil
		}
	}
	return "", fmt.Errorf("%w: no transcript in %s other than %s holds the record %s",
		ErrContinuationMissing, projectDir, leaving, target)
}

// sameFile reports whether two paths name the same transcript. Compared after
// cleaning, so a path assembled differently from the one we were handed still
// counts as the file being left — the exclusion above is load-bearing, and a
// spelling difference must not defeat it.
func sameFile(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// containsUUID reports whether any record in path has target as its own uuid —
// any record, not just the root: a logical parent points wherever the earlier
// conversation had got to, which is somewhere in the middle of it.
//
// An unreadable file answers false rather than erroring; see
// findTranscriptContaining on why a sibling is not fatal.
func containsUUID(path, target string) bool {
	found := false
	_ = scanFile(path, func(rec claudeRecord) bool {
		if rec.UUID == target {
			found = true
			return false
		}
		return true
	})
	return found
}
