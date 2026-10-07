// Package hookinput resolves, from a normalised hook input (harness.HookInput), the
// session record it belongs to and the directory its stores are keyed by. It is
// separate from internal/harness because it reads the transcript layer, which
// imports harness.
package hookinput

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/transcript"
)

// ErrNotASessionID: the reported session id is not a name, so no filename may
// be built from it. See Record.
var ErrNotASessionID = errors.New("sloprail: the reported session id is not a session id")

// Record is the transcript whose session this hook belongs to.
//
// A sub-agent is a session in its own right — its own record, possibly its own
// worktree, its own moment of ending — so everything the engine keys per session
// is a thing it has separately. Which means the only question that matters here
// is WHICH record identifies the caller, and a harness that reports both makes
// that a choice rather than a lookup.
//
// The sub-agent's own path wins wherever it is there. It is reported only to a
// sub-agent's hook, so its presence IS the discriminator; nothing has to be
// inferred from names or directory shapes. Preferring the parent's instead — or
// merely reading TranscriptPath, which is present on both — would resolve the
// PARENT's identity inside a sub-agent's hook, and the sub-agent's baseline,
// read mark, guardrail memory and file verdicts would all be written into the
// parent's state. When the sub-agent holds its own worktree those verdicts
// describe different content at the same paths, so they would not merely be
// shared with the parent, they would be wrong for it.
//
// When only an agent id is reported, the path is reconstructed from the
// parent's. That fallback is not tidiness: giving up and using the parent's path
// is precisely the confusion above, so the choice is between reconstructing and
// refusing, and refusing would stop a sub-agent's guardrails working at all.
// SubagentTranscriptPath refuses an id that is not a name rather than repairing
// it, so a reconstruction cannot leave the conversation's own directory.
//
// A payload naming NO path but a session id is the last case, and it is the root
// session's SessionStart: the hook fires as the session begins, which is the one
// moment the baseline most needs recording, so treating the absent field as "no
// record" would leave every session unable to take its own starting point. The
// path is reconstructed from the id and the working directory, using the
// encoding this codebase already keeps in one place for the identity walk.
//
// That last reconstruction is the only branch this process is RESPONSIBLE for
// being wrong about, which is why it alone is guarded. Everything above it is a
// path a harness handed over — authoritative, and nothing to check it against.
// A guess is different: the engine keys its baseline, its read mark and its
// verdicts on what comes back, so a guess that lands on the wrong real file is
// silent corruption rather than a loud failure.
//
// Three things guard it, and each catches something the others cannot:
//
//   - A session id carrying a path separator is refused rather than repaired.
//     "../-other-project/secret" joined onto the project directory is cleaned by
//     filepath.Join AFTER the concatenation, so the traversal lands in another
//     project's directory and resolves that conversation's identity with no
//     error at all. filepath.Base would make the path safe and the anomaly
//     invisible. Both slashes are refused because Windows separates on both.
//     "." and ".." need no clause: the suffix defuses them before the join, so
//     "." becomes "..jsonl" and ".." becomes "...jsonl", ordinary filenames
//     inside the project directory. A clause for them could never fire.
//   - BelongsToSession asks the file whether it is this session's. A guessed
//     name colliding with another conversation's transcript otherwise resolves
//     silently and hands back that conversation's identity.
//   - BelongsToTree asks whether it was written in this tree. EncodeProjectDir
//     maps every non-alphanumeric byte to "-", so a separator and a literal
//     hyphen become the same character: cwd "/home/u/proj" with subdirectory
//     "pkg" and the sibling checkout "/home/u/proj-pkg" both encode to
//     "-home-u-proj-pkg". If that sibling has a session by this id the guess
//     lands on its real transcript and the SESSION check agrees, because the id
//     really is that file's own. Only the tree disagrees. Two hyphenated sibling
//     checkouts is an ordinary layout, not an attack.
//
// Neither check subsumes the other: the session check catches a filename
// colliding with an unrelated conversation in the SAME tree, the tree check
// catches the same id existing in a colliding directory. What the tree check
// still cannot separate is stated on BelongsToTree, and the honest summary is
// that it narrows the collision rather than closing it — a sibling session that
// genuinely ran in this tree is indistinguishable by anything written down.
//
// The tree check sits on this branch and NOT on the reported-path branches
// above, which is what keeps it compatible with an isolated sub-agent. A
// sub-agent dispatched into its own worktree records that worktree as its cwd
// while the harness still nests its record under the DISPATCHING session's
// project directory: of 337 real sub-agent transcripts carrying a cwd, 18 record
// a sibling worktree not under the parent's tree at all, and asking
// BelongsToTree about those would refuse a sub-agent its own record. It is
// asked only of the SESSION's record: a sub-agent's path is REPORTED by
// agent_transcript_path, or reconstructed from agent_id against the session's
// record — the reported transcript_path, or, when a sub-agent's call carries
// none, the one this branch reconstructs from the session id in the tree the
// call reports (so an isolated sub-agent naming only its agent id resolves
// nothing here rather than a wrong record). projectDirOf is the same fact from the other side: for a
// sub-agent the transcript's own LOCATION is authoritative and the recorded cwd
// is not.
// sr:invariant subagents/own-session
func Record(p harness.HookInput) (string, error) {
	if p.AgentTranscriptPath != "" {
		// Nested under the session's record, so it moves with it when the
		// session was resumed from another directory — found by the same
		// lookup, or the sub-agent's hooks would key on a path that does not
		// exist while its pre-tool calls, reconstructed from agent_id against
		// the relocated session record, keyed on the real one.
		return relocate(p, p.AgentTranscriptPath), nil
	}
	root, err := SessionRecord(p)
	if err != nil || root == "" || p.AgentID == "" {
		return root, err
	}
	// A sub-agent's call reported by its agent id alone: its record is
	// reconstructed from the session's — reported, or derived from the session
	// id when the payload carries no transcript_path (a sub-agent's PreToolUse
	// can arrive that way). Answering with the session's own record here would
	// key the sub-agent's call to the ROOT's identity and state, while the same
	// sub-agent's SubagentStop, which reports agent_transcript_path, keys to its
	// own: what its pre-tool calls recorded (the citations a change was grounded
	// in) would then be in a store its own cycle never reads.
	return transcript.SubagentTranscriptPath(root, p.AgentID)
}

// relocate finds a reported record the harness wrote somewhere other than
// where it reported it (transcript.RelocateRecord) — except at a FRESH
// session's SessionStart, whose record does not exist yet by design: searching
// for it there would only ever find another project's transcript that
// happens to share a fixed --session-id. Both "startup" and "clear" are fresh
// in this sense — "clear" is a fresh session too (the previous conversation is
// discarded), reported under a --session-id that may just as well be reused,
// so it carries the identical hazard "startup" does.
func relocate(p harness.HookInput, path string) string {
	if p.Source == "startup" || p.Source == "clear" {
		return path
	}
	return transcript.RelocateRecord(transcript.ConfigDir(), path)
}

// SessionRecord is the SESSION's own record as the payload names it — its
// transcript_path, or, failing that, reconstructed from the session id — with
// no regard to a sub-agent: Record() builds a sub-agent's path on top of it.
func SessionRecord(p harness.HookInput) (string, error) {
	if p.TranscriptPath != "" {
		// Reported, so authoritative about WHICH session — but not always about
		// where its file is: a session resumed from another directory is
		// reported under that directory's project folder while its record stays
		// where it began. See transcript.RelocateRecord.
		return relocate(p, p.TranscriptPath), nil
	}
	if loc, ok := harness.Current().(harness.TranscriptLocator); ok {
		// The harness can find a record its payload did not name (harness.TranscriptLocator).
		if path := loc.LocateTranscript(p); path != "" {
			return path, nil
		}
	}
	if p.SessionID == "" {
		// No path and no id: nothing to resolve and nothing to guess from. Not a
		// fault in itself — the caller decides whether it can proceed without one.
		return "", nil
	}
	if strings.ContainsAny(p.SessionID, `/\`) {
		return "", fmt.Errorf("%w: %q", ErrNotASessionID, p.SessionID)
	}
	dir := transcript.ProjectDir(transcript.ConfigDir(), p.Cwd)
	if dir == "" {
		return "", nil
	}
	path := filepath.Join(dir, p.SessionID+".jsonl")
	if ok, err := transcript.BelongsToSession(path, p.SessionID); !ok {
		return "", err
	}
	if ok, err := transcript.BelongsToTree(path, p.Cwd); !ok {
		return "", err
	}
	return path, nil
}

// StateCwd is the directory this payload's session stores are keyed by: for a root,
// where its record says it began (so an agent that `cd`s into another worktree keeps
// its verdicts, baseline and counters); for a sub-agent, its own cwd. See
// sessionpath.StateCwd.
func StateCwd(p harness.HookInput) string {
	record, err := Record(p)
	if err != nil {
		return p.Cwd
	}
	return sessionpath.StateCwd(record, p.Cwd)
}
