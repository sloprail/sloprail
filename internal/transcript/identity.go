package transcript

import (
	"errors"
	"fmt"
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

// ErrWrongSession: a transcript was reached by GUESSING its filename from a
// session id, and the file that turned up belongs to a different conversation.
//
// Only ever the answer for a guess. A path handed over by the harness is
// authoritative and is never checked this way.
var ErrWrongSession = errors.New("the transcript at that path belongs to another session")

// ErrWrongTree: a transcript was reached by GUESSING its filename, and the file
// that turned up was written in a different working tree than the one asking.
//
// Only ever the answer for a guess, for the same reason as ErrWrongSession.
var ErrWrongTree = errors.New("the transcript at that path was written in another tree")

// BelongsToTree reports whether the transcript at path was written in cwd, or
// anywhere beneath it — compared as literal paths, not as encoded directory
// names.
//
// This exists because EncodeProjectDir is LOSSY: `[^a-zA-Z0-9] -> "-"` maps the
// separator and a literal hyphen onto the same character, so
// "/home/u/proj" + subdir "pkg" and the sibling checkout "/home/u/proj-pkg"
// both encode to "-home-u-proj-pkg". Two hyphenated sibling checkouts is an
// ordinary layout, not an attack. Verified collisions: /a/b-c ≡ /a/b/c,
// /a/b_c ≡ /a/b-c, /a/b.c ≡ /a/b/c, "/home/u/x y" ≡ /home/u/x-y.
//
// BelongsToSession does NOT catch this. When the guess lands in the sibling's
// directory and that sibling genuinely has a session by that id, the file's own
// records carry the id being asked about — it agrees, because the id really is
// its own. The disagreement is about the TREE, which that check never looks at,
// so the answer comes back as another project's real transcript with no error.
//
// The records' own cwd is what settles it. Across 8085 real transcripts 7941
// carry a cwd, first appearing by line 3 in 7522 of them. It is NOT stable
// within a file — 57 carry several distinct values, because the harness records
// the directory each turn ran in and a session that cd's into a subdirectory
// writes each one. So this asks whether ANY record's cwd is the tree in
// question or sits beneath it, rather than testing the first record: on that
// survey the rule accepts 7941 and falsely refuses 0. Testing only the first
// cwd would refuse 7.
//
// A file with no cwd on any record answers true, for the same reason
// BelongsToSession passes a file with no sessionId: 144 transcripts carry none,
// the field is observed rather than promised, and absence of evidence must not
// turn every session into a refusal. What is caught is a positive disagreement.
//
// An unreadable file answers true and leaves that failure to whoever reads it
// properly.
//
// What this does NOT catch, having been tried:
//
//   - An empty cwd answers true, because there is no tree to disagree with. The
//     caller's guess is then unguarded by this check — though ProjectDir("")
//     names the bare projects directory, which is not where transcripts live.
//   - A session in the colliding sibling that GENUINELY cd'd into this tree
//     records this tree's cwd, matches, and is accepted. Not a false positive
//     in the file's own terms — it really did run there — but the two cannot be
//     told apart by what is written down.
//   - A record longer than maxRecordBytes stops the scan, which answers true.
//     Same gap as BelongsToSession, and stated there.
func BelongsToTree(path, cwd string) (bool, error) {
	if cwd == "" {
		return true, nil
	}
	// The comparison is on the RESOLVED LITERAL path, never on the encoding.
	// Comparing encodings would be circular: the encoding is the very thing
	// that collides, so "/home/u/proj/pkg" and "/home/u/proj-pkg" would agree
	// and the check would pass exactly the case it exists to catch. Symlinks
	// are resolved on both sides because the harness records its own resolved
	// directory and macOS symlinks /var to /private/var.
	want := ResolveWorkDir(cwd)
	var found string
	err := scanFile(path, func(rec claudeRecord) bool {
		if rec.Cwd == "" {
			return true
		}
		if sameTree(ResolveWorkDir(rec.Cwd), want) {
			found = rec.Cwd
			return false
		}
		if found == "" {
			found = rec.Cwd
		}
		return true
	})
	if err != nil {
		return true, nil
	}
	if found == "" || sameTree(ResolveWorkDir(found), want) {
		return true, nil
	}
	return false, fmt.Errorf("%w: %s was written in %s, which is not %s",
		ErrWrongTree, path, found, cwd)
}

// sameTree reports whether a recorded working directory is the one being asked
// about, or somewhere beneath it.
//
// Beneath counts because the guess is keyed on the project directory of the
// tree, and a session that cd's into a subdirectory still belongs to that tree
// — 57 of 8085 real transcripts record several nested directories for exactly
// that reason. The prefix is taken component-wise, so "/a/b" does not swallow
// the sibling "/a/bc".
func sameTree(rec, want string) bool {
	return rec == want || strings.HasPrefix(rec, want+string(filepath.Separator))
}

// BelongsToSession reports whether the transcript at path was written under
// sessionID, according to the file's own records.
//
// This is the check a GUESS needs and a given path does not. Reconstructing
// "<project dir>/<session id>.jsonl" is an assumption about where a harness
// puts things, and the reasoning that a wrong guess "fails loudly" only covers
// a guess landing on nothing. A guess landing on a file that EXISTS but belongs
// to another conversation resolves silently and hands back that conversation's
// identity — after which the engine keys its state on it.
//
// Records that carry a sessionId say who wrote them, so the file can be asked
// rather than trusted to match its own name. Not every record carries one:
// across 8085 real transcripts, `file-history-snapshot` records carry only
// {isSnapshotUpdate, messageId, snapshot, type} and appear as early as line 2.
// So the scan SKIPS blank-sessionId records rather than concluding from the
// first record — otherwise a mismatch would hide behind a leading fieldless one.
//
// And a DISAGREEING record is not enough either: the scan keeps going until it
// finds agreement, and only reports the disagreement if none is ever found. One
// real transcript in this corpus opens with 303 records carrying an OLDER
// session's id and only reaches its own on line 304 — a resumed conversation
// whose earlier records were copied forward. Stopping at the first id present
// refuses that file, which is a legitimate session denied its own record. Over
// the whole corpus the first-record rule falsely refuses 1 and this one
// refuses 0.
//
// Depended on, and it is HARNESS BEHAVIOUR rather than anything guaranteed: a
// refusal here can only fire if NO record in a transcript names the session its
// filename does. Across 8109 real transcripts that holds in every one — though
// it is a near thing, since one of them carries 303 records of an older
// session's id before reaching its own. So on real forks this check never
// fires: the convergence tests pass because the harness keeps a file's own id
// somewhere in its records, not because anything forces it to. If that ever
// changes, this turns from inert into a refusal on every fork, which is why it
// is written down here rather than assumed.
//
// A file carrying no sessionId at all answers true. The field is observed
// rather than promised, and a harness that stops writing it must not turn every
// session into a refusal — the check is here to catch a guess landing on
// someone ELSE's conversation, which is a positive disagreement, not an absence
// of evidence. Both the check and its limit are the point: what is caught is a
// file naming a different session, and what is not caught is a file naming no
// session.
//
// An unreadable file answers true and leaves the failure to whoever reads it
// properly, which reports the open error with its own path in it. This covers
// one real gap: a record longer than maxRecordBytes stops the scan with an
// error, so a mismatch sitting BEHIND such a line is not seen and the file is
// allowed. Accepted rather than fixed, because the alternative — refusing every
// file with one oversized record — turns a formatting accident into a dead
// session. BelongsToTree has the same gap on the same line, so the two do not
// cover for each other here; this is a limit of both, not of one.
func BelongsToSession(path, sessionID string) (bool, error) {
	if sessionID == "" {
		return true, nil
	}
	var found string
	err := scanFile(path, func(rec claudeRecord) bool {
		if rec.SessionID == "" {
			return true
		}
		if rec.SessionID == sessionID {
			found = rec.SessionID
			return false
		}
		if found == "" {
			found = rec.SessionID
		}
		return true
	})
	if err != nil {
		return true, nil
	}
	if found == "" || found == sessionID {
		return true, nil
	}
	return false, fmt.Errorf("%w: %s says it belongs to %s, not %s",
		ErrWrongSession, path, found, sessionID)
}
