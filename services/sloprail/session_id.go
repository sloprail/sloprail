package main

import (
	"fmt"

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
// keeping a second assumption about where that harness puts things. The project
// directory IS derived, because crossing a restart means reading the
// conversation's other transcripts and no payload names those.
//
// The error is not swallowed. A hook runs where the harness's transcript must
// exist, so failing to resolve means the environment is broken — and falling
// back to the reported id would restore the exact silent orphaning this exists
// to prevent.
func stableID(p HookPayload) (string, error) {
	if p.TranscriptPath == "" {
		return "", fmt.Errorf("sloprail: no transcript path on the hook payload — the record of this session is what its identity is read from")
	}
	projectDir := transcript.ProjectDir(transcript.ConfigDir(), p.Cwd)
	return transcript.StableSessionID(projectDir, p.TranscriptPath)
}
