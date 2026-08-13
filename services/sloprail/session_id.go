package main

import (
	"fmt"
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
// session's, and resolves it to something distinct: across the 305 real
// sub-agent transcripts on one machine, all 305 resolved, all 305 were distinct
// from one another, none equalled its parent's, and none collided with any of
// the 7,943 main-transcript origins. Extending the walk for sub-agents would
// have been a fourth derivation of session identity on a project that has
// already had to converge three.
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
func stableID(p HookPayload) (string, error) {
	path, err := p.record()
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("sloprail: no transcript path on the hook payload — the record of this session is what its identity is read from")
	}
	return transcript.StableSessionID(projectDirOf(path, p.Cwd), path)
}

// projectDirOf is where the conversation's OTHER transcripts live, given the
// path of this one.
//
// Taken from the transcript's own location wherever that is knowable, because
// the transcript is already in the directory being looked for and a path is a
// fact where an encoded working directory is an assumption. A sub-agent's record
// sits one level deeper — <project>/<session>/subagents/agent-<id>.jsonl — so
// the directory is reached by climbing out of the two levels the harness nested
// it in, rather than by encoding a cwd that for an isolated sub-agent names
// somewhere else entirely.
//
// Falls back to encoding the working directory when the path is empty, which is
// the SessionStart case: the hook fires as the session begins and no record has
// been written yet.
func projectDirOf(path, cwd string) string {
	if path == "" {
		return transcript.ProjectDir(transcript.ConfigDir(), cwd)
	}
	dir := filepath.Dir(path)
	// A sub-agent's record is nested in <session>/subagents/; the conversation's
	// other transcripts are two levels up, beside the session's own file.
	if filepath.Base(dir) == transcript.SubagentDir {
		return filepath.Dir(filepath.Dir(dir))
	}
	return dir
}
