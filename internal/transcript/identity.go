package transcript

import "fmt"

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
		return "", fmt.Errorf("transcript: stable session id: %w", ErrNoTranscriptPath)
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
			return "", fmt.Errorf("transcript: stable session id: %s continues %s: %w",
				cur, root.LogicalParentUUID, ErrNoProjectDir)
		}
		next, err := findTranscriptContaining(projectDir, root.LogicalParentUUID, cur)
		if err != nil {
			return "", fmt.Errorf("transcript: stable session id: resolving what %s continues: %w", cur, err)
		}
		if visited[next] {
			return "", fmt.Errorf("transcript: stable session id: the chain from %s revisits %s: %w",
				path, next, ErrChainRunaway)
		}
		visited[next] = true
		cur = next
	}
	return "", fmt.Errorf("transcript: stable session id: the chain from %s ran past %d restarts: %w",
		path, maxRestartHops, ErrChainRunaway)
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
		return Entry{}, fmt.Errorf("%s: %w: every entry in it has a parent", path, ErrNoOriginRecord)
	}
	return root, nil
}
