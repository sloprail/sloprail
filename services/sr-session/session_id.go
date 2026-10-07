package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/transcript"
)

// newSessionIDCmd prints the identity the conversation keeps.
//
// Not the id the harness reports. Claude Code re-forks that mid-conversation
// and writes a new transcript sharing nearly all its history with the old one,
// so anything keyed on it starts empty at that moment and abandons the
// baseline and every verdict recorded so far — without erroring, and in the
// middle of a session. What stays put is where the conversation began.
//
// Everything the engine stores per session is found again under this, which is
// why it is a command rather than an internal detail: a hook script keying its
// own state must key it the same way, or it silently keeps two sessions'
// worth of memory for one conversation.
func newSessionIDCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "id",
		Short: "The identity this conversation keeps, whatever id the harness now reports",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := readPayload(cmd)
			id, err := stableID(p)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), id)
			return nil
		},
	}
}

// stableID resolves the conversation's identity from what the harness reported.
//
// The transcript's path comes off the payload rather than being derived: the
// harness hands it over on every invocation, and deriving it instead would mean
// keeping a second assumption about where that harness puts things. Which of the
// reported paths is the caller's own is HookPayload.record's decision — a
// sub-agent is a session in its own right, and reading the parent's record in a
// sub-agent's hook would key the sub-agent's state by the parent's name.
//
// A sub-agent needs no separate walk. Its transcript carries its own parentless
// origin record, so StableSessionID resolves it exactly as it resolves a root
// session's, and resolves it to something distinct: across the 331 real
// sub-agent transcripts on one machine, every origin record carried a uuid and
// an explicitly null parentUuid, all resolved, all were distinct from one
// another, none equalled its parent's, and none collided with any of the 8,119
// main-transcript origins. Extending the walk for sub-agents would have been a
// fourth derivation of session identity on a project that has already had to
// converge three.
//
// The project directory IS derived, because crossing a restart means reading the
// conversation's other transcripts and no payload names those. It is derived
// from the transcript's own location rather than from the reported working
// directory, which matters precisely here: a sub-agent dispatched into an
// isolated worktree reports THAT as its cwd, while the harness still nests its
// record under the dispatching session's project directory. Encoding the
// sub-agent's cwd would name a directory the transcripts are not in, and a
// restart could then not be crossed.
//
// The error is not swallowed. A hook runs where the harness's transcript must
// exist, so failing to resolve means the environment is broken — and falling
// back to the reported id would restore the exact silent orphaning this exists
// to prevent.
//
// The path is taken from p.Record() rather than from the field, so a payload
// that names the session without naming its file still resolves. That is the
// SessionStart payload, and it is the one place where failing to resolve costs
// the session its baseline. Whatever that resolution refuses is refused here
// too, unchanged: a session id that is not a name, a guessed file belonging to
// another conversation, and a guessed file written in another tree are all cases
// where carrying on means keying this session's state on somebody else's
// identity — the same silent orphaning this function exists to prevent, arrived
// at from the other side.
func stableID(p HookPayload) (string, error) {
	id, err := stableIdentity(p)
	return id.ID, err
}

// stableIdentity is stableID with the walk's Degraded flag kept: whether the id
// is the conversation's origin or the continuation root the walk fell back to.
// See transcript.Identity for the tradeoff, and noteDegradedIdentity for how
// it is surfaced. The walk itself, and its per-process memory, are
// sessionpath.StableIdentity — shared with sr-checks.
func stableIdentity(p HookPayload) (transcript.Identity, error) {
	path, err := p.Record()
	if err != nil {
		return transcript.Identity{}, err
	}
	if path == "" {
		return transcript.Identity{}, fmt.Errorf("sloprail: no transcript path on the hook payload — the record of this session is what its identity is read from")
	}
	return sessionpath.StableIdentity(path, p.Cwd)
}

// degradedMarker prefixes the files, beside a session's state, that record a
// harness session was told its identity is a fallback.
const degradedMarker = "identity-degraded."

// noteDegradedIdentity tells the person, once per harness session, that the
// session's identity is a fallback rather than its conversation's origin. It
// is called at SessionStart and nowhere else.
//
// At SessionStart because that is the one hook whose output reaches anyone:
// its stdout is added to the agent's context and rendered in the transcript,
// where every other hook that exits 0 has its stderr recorded but shown to
// nobody. A notice printed first by a pre-tool or Stop hook would be the one
// report — and invisible — and the marker would then silence every later
// hook. SessionStart fires on every startup, resume and compaction, so a
// session whose identity is degraded always passes through one: a resumed
// continuation whose predecessor is gone, or one whose predecessor was deleted
// while it was closed, hears it on resume.
//
// Once per HARNESS SESSION, not once per identity. Every fork of a degraded
// continuation shares the fallback id, and so the state directory the marker
// sits in — but each fork is a session somebody started or resumed, and should
// hear why it does not have what came before. So the marker is named by the
// harness's session id (identity-degraded.<session id>). Within one session
// the condition is permanent — the transcript it continues is not coming back —
// and a compaction's SessionStart does not repeat it. A marker that cannot be
// created for any reason but already existing is no reason to stay quiet, so
// that case still prints.
//
// Not a refusal. The session keeps working under the fallback; what it loses is
// only what was stored before the continuation, and that is what the notice
// says. Written to stdout (the agent's context, which the person sees) and to
// stderr (recorded with the hook's run).
func noteDegradedIdentity(stdout, stderr io.Writer, p HookPayload, id transcript.Identity) {
	if id.Degraded == nil || id.ID == "" {
		return
	}
	if session := harnessSessionID(p); session != "" {
		if db, err := sessionDBPath(p.StateCwd(), id.ID); err == nil {
			marker := filepath.Join(filepath.Dir(db), degradedMarker+session)
			if mkErr := os.MkdirAll(filepath.Dir(marker), 0o755); mkErr == nil {
				f, openErr := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
				if errors.Is(openErr, fs.ErrExist) {
					return
				}
				if openErr == nil {
					fmt.Fprintln(f, id.Degraded)
					f.Close()
				}
			}
		}
	}
	why := "whose earlier transcript is gone"
	switch {
	case errors.Is(id.Degraded, transcript.ErrChainRunaway):
		why = "whose chain of earlier transcripts loops or runs past any real conversation's length"
	case len(id.UnreadableSiblings) > 0:
		why = "whose earlier transcript could not be reached — some of this project's other transcripts could not be read"
	}
	msg := fmt.Sprintf("sloprail: identity: this session continues a conversation %s, "+
		"so its state is kept under the continuation (%s) rather than the conversation's origin; "+
		"anything recorded before that continuation is not carried over. (%v)", why, id.ID, id.Degraded)
	fmt.Fprintln(stdout, msg)
	fmt.Fprintln(stderr, msg)
}

// harnessSessionID is the id the harness reports for this session — the
// payload's, or the record's own file name.
func harnessSessionID(p HookPayload) string {
	if p.SessionID != "" && !strings.ContainsAny(p.SessionID, `/\`) {
		return p.SessionID
	}
	if p.TranscriptPath != "" {
		return strings.TrimSuffix(filepath.Base(p.TranscriptPath), ".jsonl")
	}
	return ""
}

// projectDirOf is where the conversation's OTHER transcripts live, given the
// path of this one — sessionpath.ProjectDirOf, shared with sr-checks.
var projectDirOf = sessionpath.ProjectDirOf
