package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The identity a conversation keeps.
//
// A harness's own session id is not that identity. Claude Code re-forks it
// mid-conversation — on a retried request, and separately some time after a
// compaction — writing a fresh transcript that shares nearly all of its history
// with the one before; a10n observed two such transcripts sharing 1501 of 1585
// records. Anything keyed on the reported id follows that fork into an empty
// directory and abandons the work recorded under the old one, without failing
// and without saying so.
//
// What stays put is where the conversation began. A transcript has exactly one
// record with no parent, and a forked transcript continues the same message
// tree, so both carry the same origin. This is not a heuristic and not a time
// window: an unrelated conversation has its own distinct origin by
// construction. a10n verified it across 29 real transcripts — two independent
// fork pairs shared a root uuid with each other and with nothing else.

// maxRestartHops bounds the walk across restarts. A real conversation resumes a
// handful of times, never hundreds, so this never binds in practice — it is
// there so a malformed chain fails with a diagnosis instead of spinning.
const maxRestartHops = 64

// StableSessionID resolves the identity of the conversation the transcript at
// path belongs to.
//
// projectDir is where the conversation's OTHER transcripts live — needed only
// to cross a restart, since the record a restart continues from is by
// definition in an older file. Derive it with ProjectDir rather than by
// searching: see path.go on the 1067-directory sweep that replaces.
//
// Failure is loud. This runs where a harness's transcript must exist, so a
// missing or unreadable one is a broken environment rather than a session
// without a record. Returning the reported id instead would restore the exact
// silent orphaning this exists to prevent, and would do it invisibly — the
// caller would carry on, quietly, against the wrong session.
func StableSessionID(projectDir, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("transcript: stable session id: no transcript path")
	}

	visited := map[string]bool{path: true}
	cur := path
	for hop := 0; hop < maxRestartHops; hop++ {
		root, err := rootRecord(cur)
		if err != nil {
			return "", fmt.Errorf("transcript: stable session id: %w", err)
		}

		// The first walk ended at a record with no parent. If it carries no
		// logical parent either, it is the true origin and there is nothing
		// older to reach.
		if root.LogicalParentUUID == "" {
			return root.UUID, nil
		}

		// The second walk. This root only opens a file continuing an earlier
		// one, so the conversation began further back — in whichever older
		// transcript holds the record it resumes.
		if projectDir == "" {
			return "", fmt.Errorf("transcript: stable session id: %s continues %s but the harness's project directory is unknown, so the older transcript cannot be found",
				cur, root.LogicalParentUUID)
		}
		next, err := findTranscriptContaining(projectDir, root.LogicalParentUUID, cur)
		if err != nil {
			return "", fmt.Errorf("transcript: stable session id: resolving what %s continues: %w", cur, err)
		}
		if visited[next] {
			return "", fmt.Errorf("transcript: stable session id: the chain from %s revisits %s", path, next)
		}
		visited[next] = true
		cur = next
	}
	return "", fmt.Errorf("transcript: stable session id: the chain from %s ran past %d restarts, which a real conversation does not — it is malformed or it loops",
		path, maxRestartHops)
}

// rootRecord returns the first record in path that has no parent — the
// conversation's root within this file — along with what it continues from, if
// anything.
//
// It keeps scanning past a uuid-carrying record that already has a parent
// rather than giving up on the first one. In every observed transcript the root
// is the first uuid-carrying record (the preamble lines before it carry no
// uuid at all), but a file with unusual leading records still resolves.
//
// No root before the end of the file is an error, never a silent empty answer:
// a transcript in which every record has a parent is not a session we
// understand, and guessing at one would key state on a guess.
func rootRecord(path string) (Entry, error) {
	var root Entry
	found := false
	err := scanFile(path, func(rec claudeRecord) bool {
		if rec.UUID == "" || rec.ParentUUID != nil {
			return true
		}
		root = rec.entry()
		found = true
		return false
	})
	if err != nil {
		return Entry{}, err
	}
	if !found {
		return Entry{}, fmt.Errorf("%s has no origin record: every entry in it has a parent", path)
	}
	return root, nil
}

// findTranscriptContaining returns the transcript in projectDir holding a record
// whose own uuid is target, skipping leaving.
//
// leaving is always the file whose logical parent is being resolved, and
// skipping it is not tidiness — it is the difference between crossing a restart
// and going round in a circle. Compaction copies earlier records verbatim into
// the new file, uuids included, so the record a boundary points at is very
// often also present in the file doing the pointing. This is observed rather
// than theorised: in a real transcript on this machine, a compact_boundary's
// logicalParentUuid names its own preserved segment's tail, and that uuid
// appears 2500 lines further down the very same file. Matching it there sends
// the walk straight back to the root it just read.
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
	return "", fmt.Errorf("no transcript in %s other than %s holds the record %s", projectDir, leaving, target)
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
