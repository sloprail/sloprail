package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// A session with no transcript: codex --ephemeral reports transcript_path null on every hook, and
// claude --no-session-persistence reports the path the session WOULD have written, a file that
// never exists. Everything sloprail judges from the session record (citations, grounding judges,
// the trajectory, state keyed by the record) has nothing to read, and a refusal could not be
// fixed by the agent, so refusing would only deadlock the run. The engine switches itself off for
// the session instead, and says so, loudly, once.

// noTranscriptNotice is what the person is told. A systemMessage is shown by the harness; the
// same text goes to stderr for a log.
const noTranscriptNotice = "sloprail: guardrails are OFF for this session: the harness keeps no transcript for it " +
	"(an ephemeral or no-session-persistence run), and the rules that read the session record cannot run. " +
	"No rule will fire and nothing will be refused."

// sessionHasNoTranscript reports whether the hook belongs to a session whose transcript does not
// exist and will not. atStart is SessionStart: a fresh session's file is not written yet there, so
// a path to a missing file proves nothing; a payload with no path at all, and a path to a missing
// file at any later hook, do.
//
// Without a session id (the load check, a hand-run command) there is no session to disable. A
// payload whose record cannot be resolved for another reason is left to its own error.
func sessionHasNoTranscript(p HookPayload, atStart bool) bool {
	if p.SessionID == "" {
		return false
	}
	path, err := p.sessionRecord()
	if err != nil {
		return false
	}
	if path == "" {
		return true
	}
	if atStart {
		return false
	}
	_, err = os.Stat(path)
	return errors.Is(err, fs.ErrNotExist)
}

// skipWithoutTranscript is the guard every hook that runs rules starts with: when the session has
// no transcript it says so (once per session on the screen, always on stderr) and returns true,
// and the hook does nothing more.
func skipWithoutTranscript(cmd *cobra.Command, p HookPayload, atStart bool) bool {
	if !sessionHasNoTranscript(p, atStart) {
		return false
	}
	fmt.Fprintln(cmd.ErrOrStderr(), noTranscriptNotice)
	if firstNoTranscriptNotice(p.SessionID) {
		_ = json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"systemMessage": noTranscriptNotice})
	}
	return true
}

// firstNoTranscriptNotice reports whether this session has not been told yet, and records that it
// now has. A marker that cannot be kept means telling again, never staying silent.
func firstNoTranscriptNotice(sessionID string) bool {
	if strings.ContainsAny(sessionID, `/\`) {
		return true
	}
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		return true
	}
	dir := filepath.Join(base, "sloprail", "no-transcript")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return true
	}
	f, err := os.OpenFile(filepath.Join(dir, sessionID), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return !errors.Is(err, fs.ErrExist)
	}
	f.Close()
	return true
}
