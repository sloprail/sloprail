package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

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
// The path is taken from p.record() rather than from the field, so a payload
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
// is the conversation's origin or the continuation root the walk fell back to
// because the transcript it continues is gone. See transcript.Identity for the
// tradeoff, and noteDegradedIdentity for how it is surfaced.
func stableIdentity(p HookPayload) (transcript.Identity, error) {
	path, err := p.record()
	if err != nil {
		return transcript.Identity{}, err
	}
	if path == "" {
		return transcript.Identity{}, fmt.Errorf("sloprail: no transcript path on the hook payload — the record of this session is what its identity is read from")
	}
	return transcript.ResolveStableSessionID(projectDirOf(path, p.Cwd), path)
}

// degradedMarker is the file, beside a session's state, that records the
// session was told its identity is a fallback.
const degradedMarker = "identity-degraded"

// noteDegradedIdentity tells the person, ONCE per session, that this session's
// identity is a fallback rather than its conversation's origin.
//
// Once, because the condition is permanent for the session — the transcript it
// continues is not coming back — and every hook resolves it again. Printed on
// every hook it would be the noise the identity errors this replaces were: 225
// real hook runs repeated the same line into the transcript. The first hook to
// see it creates a marker beside the session's state and says so; later hooks
// find the marker and stay quiet. A marker that cannot be created for any other
// reason than already existing is no reason to stay quiet, so that case still
// prints.
//
// Not a refusal. The session keeps working under the fallback; what it loses is
// only what was stored before the continuation, and that is what the line says.
func noteDegradedIdentity(w io.Writer, p HookPayload, id transcript.Identity) {
	if id.Degraded == nil || id.ID == "" {
		return
	}
	db, err := sessionDBPath(p.Cwd, id.ID)
	if err == nil {
		marker := filepath.Join(filepath.Dir(db), degradedMarker)
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
	fmt.Fprintf(w, "sloprail: this session continues a conversation whose earlier transcript is gone, "+
		"so its state is kept under the continuation (%s) rather than the conversation's origin; "+
		"anything recorded before that continuation is not carried over. (%v)\n", id.ID, id.Degraded)
}

// projectDirOf is where the conversation's OTHER transcripts live, given the
// path of this one.
//
// Taken from the transcript's own location wherever that is knowable, because
// the transcript is already in the directory being looked for and a path is a
// fact where an encoded working directory is an assumption. A sub-agent's record
// is nested under <project>/<session>/subagents/, so the directory is reached by
// climbing back out of that nesting rather than by encoding a cwd that for an
// isolated sub-agent names somewhere else entirely.
//
// How FAR out is not a constant, which is why the climb is delegated rather than
// written here as two calls to filepath.Dir. Real data carries a second layout,
// <session>/subagents/workflows/wf_<id>/agent-<id>.jsonl, one a fixed two-level
// climb resolves to <session>/subagents — a directory holding no transcripts at
// all, so the conversation's history would read as empty. SessionDirOfSubagent
// finds the subagents component instead of counting to it; see its note.
//
// Falls back to encoding the working directory when the path is empty, which is
// the SessionStart case: the hook fires as the session begins and no record has
// been written yet.
func projectDirOf(path, cwd string) string {
	if path == "" {
		return transcript.ProjectDir(transcript.ConfigDir(), cwd)
	}
	// A sub-agent's record is nested under <session>/subagents/; the
	// conversation's other transcripts sit beside the session's own file, one
	// level above that directory.
	if sessionDir := transcript.SessionDirOfSubagent(path); sessionDir != "" {
		return filepath.Dir(sessionDir)
	}
	return filepath.Dir(path)
}
