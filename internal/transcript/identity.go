package transcript

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sloprail/sloprail/internal/harness"
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

// maxRestartHops bounds how far back the walk goes. A real conversation
// resumes a handful of times, never hundreds, so this never binds in practice;
// a chain that reaches it is malformed and degrades like a ring (see Identity).
const maxRestartHops = 64

// maxWalkVisits bounds the total number of transcripts the walk will open as
// candidates, across all the branches it backtracks through. Backtracking makes
// the walk a search, and a search needs a budget; a real walk opens a handful.
const maxWalkVisits = 512

// Identity is the conversation identity a transcript resolves to.
//
// ID is always usable: every hook of a session gets the same one for as long as
// its transcript exists. Degraded says whether it is the conversation's TRUE
// origin or the best the walk could do.
type Identity struct {
	// ID is the uuid the session's state is keyed on.
	ID string

	// Degraded is nil when ID is the conversation's origin. It is set when the
	// walk reached a continuation it could not cross, and ID is then that
	// continuation's own root, the furthest-back record the walk reached. The
	// error says why it stopped: ErrContinuationMissing when no transcript on
	// disk holds what the continuation names (the predecessor was deleted, or
	// every candidate that held it could not be read — see UnreadableSiblings),
	// or ErrChainRunaway when the continuations close on themselves or run past
	// maxRestartHops — shapes no real harness writes, so malformed files.
	//
	// Where the walk falls back depends on where it started only for a
	// malformed ring: in a ring every file is somebody's predecessor, so the
	// walk from each file stops at a different one (from link-a it stops at the
	// file that points back to link-a). For the real case, a deleted
	// predecessor, every fork of the continuation reaches the same dead end and
	// so the same fallback.
	//
	// The tradeoff, stated because it is a real one. What the conversation
	// stored BEFORE that continuation is keyed on an origin nobody can compute
	// any more, so it is not rejoined; the session starts a fresh store at the
	// continuation. What it buys is everything after: the fallback is the
	// continuation root, which the harness writes once and never moves, and
	// which every fork of that continuation carries verbatim. So all of this
	// session's own hooks agree, and so do its forks, which is the convergence
	// the whole identity exists for. The alternative this replaces was no
	// identity at all, and a session with no store has no baseline, no recorded
	// citations and no guardrail state for the rest of its life.
	Degraded error

	// UnreadableSiblings lists every transcript the walk found in a project
	// directory it searched but could not read to a decision (a record past
	// maxRecordBytes, a permission error) — present whether or not the walk
	// resolved. A file like that is not a predecessor that is GONE: unlike a
	// deleted file, it might hold the very record being searched for, so its
	// unreadability is reported rather than silently treated as absence, but it
	// must never by itself stop the walk from finding a DIFFERENT, readable
	// predecessor that does resolve. See predecessors.
	UnreadableSiblings []string
}

// StableSessionID resolves the identity of the conversation the transcript at
// path belongs to. It is ResolveStableSessionID without the Degraded flag: the
// id a degraded walk falls back to is returned as an answer, not an error. A
// caller that surfaces the degradation uses ResolveStableSessionID.
func StableSessionID(projectDir, path string) (string, error) {
	id, err := ResolveStableSessionID(projectDir, path)
	return id.ID, err
}

// ResolveStableSessionID resolves the identity of the conversation the
// transcript at path belongs to.
//
// projectDir is where the conversation's OTHER transcripts live — needed only
// to cross a restart, since the record a restart continues from is by
// definition in an older file. Derive it with ProjectDir rather than by
// searching: see path.go on the 1067-directory sweep that replaces.
//
// What fails is what leaves nothing trustworthy to key on at all: no path, the
// STARTING transcript itself cannot be read, it has no parentless record, or a
// restart has no project directory to cross it in. A hook runs where the
// harness's own transcript must be readable, so these are a broken
// environment, and returning the reported id instead would restore the exact
// silent orphaning this exists to prevent.
//
// A candidate PREDECESSOR elsewhere in the project directory that is ON DISK
// but cannot be read (a record past maxRecordBytes, a permission error) does
// NOT fail the walk — it is not this session's own record, and one unrelated
// bad file must never take down every continuation in the directory. It is
// also not treated as "gone": unlike a deleted file it might hold the very
// record being searched for, so the walk keeps looking for a DIFFERENT,
// readable predecessor first, and only falls back to Degraded, with the path
// remembered in Identity.UnreadableSiblings, when nothing readable resolves.
// Silently calling it "gone" would key the session on a fallback today and on
// the origin tomorrow, if the file became readable, splitting its state in
// two — this is why the two are told apart.
//
// A predecessor that is genuinely gone is likewise NOT a hard failure. The
// session has a record and a continuation root, so it resolves to that root
// with Degraded set — see Identity. Measured on one ~/.claude: of 19
// transcripts opening on a continuation, 3 name a predecessor no file holds
// any more, because Claude Code deletes transcripts older than its cleanup
// period while a later continuation of the same conversation is still
// resumable.
// sr:invariant session/identity-survives-reissued-ids
func ResolveStableSessionID(projectDir, path string) (Identity, error) {
	if path == "" {
		return Identity{}, fmt.Errorf("transcript: stable session id: %w", ErrNoTranscriptPath)
	}
	root, err := rootRecord(path)
	if err != nil {
		return Identity{}, fmt.Errorf("transcript: stable session id: %w", err)
	}
	// The first walk ended at a record with no parent. If it carries no
	// logical parent either, it is the true origin and there is nothing older
	// to reach.
	if root.LogicalParentUUID == "" {
		return Identity{ID: root.UUID}, nil
	}
	// The second walk. This root only opens a file continuing an earlier one,
	// so the conversation began further back — in whichever older transcript
	// holds the record it resumes.
	if projectDir == "" {
		return Identity{}, fmt.Errorf("transcript: stable session id: %s continues %s: %w",
			path, root.LogicalParentUUID, ErrNoProjectDir)
	}
	w := &walk{dir: projectDir}
	id, err := w.from(path, root, map[string]bool{}, 0)
	if err != nil {
		return Identity{}, fmt.Errorf("transcript: stable session id: %w", err)
	}
	id.UnreadableSiblings = w.unreadable
	return id, nil
}

// walk is one resolution's search back through a project directory.
type walk struct {
	dir     string
	visits  int
	listing []candidate
	listed  bool

	// unreadable collects, once each, the path of every transcript the walk
	// SAW in the directory listing but could not read to a decision. Recorded
	// rather than raised, so one bad file never stops the search from finding
	// a different, readable predecessor — see predecessors and
	// Identity.UnreadableSiblings.
	unreadable []string
	seenUnread map[string]bool
}

// noteUnreadable records path as seen-but-unreadable, once.
func (w *walk) noteUnreadable(path string) {
	if w.seenUnread == nil {
		w.seenUnread = map[string]bool{}
	}
	if w.seenUnread[path] {
		return
	}
	w.seenUnread[path] = true
	w.unreadable = append(w.unreadable, path)
}

// candidate is a transcript in the project directory and the root it opens on.
type candidate struct {
	path string
	root Entry
}

// from resolves the continuation opening file cur with root. inPath holds the
// roots on the path from the starting file down to here: a candidate opening
// on one of them is either a sibling fork of a continuation already walked
// through or a ring, never an earlier file.
//
// It is a depth-first search. Candidates are tried in name order; the first
// whose own walk reaches a true origin is the answer. A candidate whose walk
// dead-ends further back (a deleted file, a ring) does not end the search —
// the next candidate is tried — because a file can hold a copy of the record a
// boundary names without being where the conversation came from. Only when no
// candidate reaches an origin does the walk degrade, and then to the first
// candidate's dead end (name order again, so every fork degrades alike), or to
// this continuation's own root when there is no candidate at all.
func (w *walk) from(cur string, root Entry, inPath map[string]bool, depth int) (Identity, error) {
	if depth >= maxRestartHops {
		return Identity{ID: root.UUID, Degraded: fmt.Errorf(
			"the chain from %s ran past %d restarts: %w", cur, maxRestartHops, ErrChainRunaway)}, nil
	}
	cands, err := w.predecessors(root)
	if err != nil {
		return Identity{}, fmt.Errorf("resolving what %s continues: %w", cur, err)
	}
	inPath[root.UUID] = true
	defer delete(inPath, root.UUID)

	var fallback *Identity
	looped := ""
	for _, c := range cands {
		if inPath[c.root.UUID] {
			looped = c.path
			continue
		}
		if w.visits++; w.visits > maxWalkVisits {
			return Identity{ID: root.UUID, Degraded: fmt.Errorf(
				"resolving what %s continues: gave up after opening %d transcripts: %w", cur, maxWalkVisits, ErrChainRunaway)}, nil
		}
		var id Identity
		if c.root.LogicalParentUUID == "" {
			id = Identity{ID: c.root.UUID}
		} else {
			id, err = w.from(c.path, c.root, inPath, depth+1)
			if err != nil {
				return Identity{}, err
			}
		}
		if id.Degraded == nil {
			return id, nil
		}
		if fallback == nil {
			fallback = &id
		}
	}
	if fallback != nil {
		return *fallback, nil
	}
	if looped != "" {
		return Identity{ID: root.UUID, Degraded: fmt.Errorf(
			"resolving what %s continues: the only transcript holding it, %s, is one the walk already crossed: %w",
			cur, looped, ErrChainRunaway)}, nil
	}
	unreadNote := ""
	if len(w.unreadable) > 0 {
		unreadNote = fmt.Sprintf(" (%d file(s) in that directory could not be read and were skipped rather than assumed to hold it: %s)",
			len(w.unreadable), strings.Join(w.unreadable, ", "))
	}
	return Identity{ID: root.UUID, Degraded: fmt.Errorf(
		"resolving what %s continues: no readable transcript in %s opening on another root holds the record %s or %s itself%s: %w",
		cur, w.dir, root.LogicalParentUUID, root.UUID, unreadNote, ErrContinuationMissing)}, nil
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
	err := scanFile(path, func(rec harness.Record) bool {
		if rec.UUID == "" || rec.ParentUUID != nil {
			return true
		}
		root = rec.Entry()
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
	err := scanFile(path, func(rec harness.Record) bool {
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
	err := scanFile(path, func(rec harness.Record) bool {
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

// StartCwd is the working directory a session began in: the first directory its
// record names. A later record may name another (an agent that `cd`'d), and that
// is the point of asking for the first — where the session's own tree is decided
// once, not wherever the agent last stood.
//
// "" with a nil error when the record names none; an error only when it cannot be
// read. Callers that need an answer treat "" as "cannot be determined".
func StartCwd(path string) (string, error) {
	var found string
	err := scanFile(path, func(rec harness.Record) bool {
		if rec.Cwd != "" {
			found = rec.Cwd
			return false
		}
		return true
	})
	return found, err
}

// StartTime is when the record's first timestamped entry was written: when the session
// began. The zero time with a nil error when the record carries none.
func StartTime(path string) (time.Time, error) {
	var found time.Time
	err := scanFile(path, func(rec harness.Record) bool {
		if rec.Timestamp == "" {
			return true
		}
		if t, perr := time.Parse(time.RFC3339Nano, rec.Timestamp); perr == nil {
			found = t
			return false
		}
		return true
	})
	return found, err
}
