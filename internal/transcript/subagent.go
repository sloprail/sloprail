package transcript

import (
	"fmt"
	"path/filepath"
	"strings"
)

// A sub-agent is a session in its own right.
//
// It has its own record, written to its own file; it may hold its own worktree;
// and it ends at its own moment, which is not the moment the session that
// dispatched it ends. Everything the engine keys per session — the baseline, the
// read mark, a guardrail's memory, a verdict on a file — is therefore a thing a
// sub-agent has separately, and confusing the two would key one agent's work by
// the other's name.
//
// This file is the routing that makes that true. The IDENTITY needs nothing new:
// a sub-agent's transcript has its own origin record, so StableSessionID already
// resolves it, and resolves it to something that is not its parent's. That was
// verified rather than assumed — see SubagentTranscriptPath's note on the 305
// real sub-agent transcripts this was measured against. What was missing is
// knowing WHICH transcript to resolve, because a harness reports both.

// SubagentDir is the directory a harness nests a session's sub-agent records
// under, beside the session's own transcript.
//
// Observed, not promised: Claude Code writes
// <project>/<session>/subagents/agent-<agent id>.jsonl, with the session's own
// transcript at <project>/<session>.jsonl beside the directory. Named here so
// the one place that assembles such a path is the one place that has to change
// when the harness moves it.
const SubagentDir = "subagents"

// subagentFilePrefix is what a harness names a sub-agent's record file, before
// the agent's own id.
const subagentFilePrefix = "agent-"

// SubagentTranscriptPath returns where the sub-agent identified by agentID
// wrote its record, given the transcript of the session that dispatched it.
//
// Needed because a harness does not always hand the sub-agent's own path over.
// Claude Code's SubagentStop payload carries agent_transcript_path and the
// answer is simply that; but the payload also carries an agent_id in cases where
// it does not, and a sub-agent hook that fell back to the PARENT's path would
// resolve the parent's identity and write the sub-agent's baseline, read mark
// and verdicts into the parent's state. That is the confusion this whole file
// exists to prevent, so the fallback has to reconstruct rather than give up.
//
// The layout is measured. Across the 305 sub-agent transcripts in one ~/.claude,
// every file sat at <parent transcript minus .jsonl>/subagents/agent-<id>.jsonl,
// every record in every one of them carried isSidechain true, and every record's
// agentId equalled its own filename's id. None carried a logicalParentUuid, and
// all 305 origin uuids were distinct from each other and from all 7,943 main
// transcript origins.
//
// An agentID that is not a plain name is refused rather than repaired. It is
// joined onto a directory path, and filepath.Join CLEANS after concatenating, so
// an id of "../../../../../-another-project/victim" resolves out of the
// subagents directory, out of the session's, out of the project's, out of
// projects/ itself, and lands the read — and then the identity, and then all the
// state keyed on it — in a SIBLING project's conversation, with no error raised
// anywhere. Five hops rather than three because "agent-.." is one ordinary
// component: the prefix absorbs the first climb. The exact depth is pinned in
// TestSubagentTranscriptPathRefusesTraversal rather than reasoned about here.
//
// Taking filepath.Base instead would make the path safe and the anomaly
// invisible; an agent id with a separator in it is not an agent id, and saying
// so is the only honest answer. Both slashes are refused because Windows
// separates on both, and "." and ".." are refused by name because they contain
// no separator yet still traverse.
//
// Empty parentPath or agentID yields an error for the same reason: a path
// assembled from a missing piece is a guess, and a guess about where state lives
// is how one session's work lands under another's name.
func SubagentTranscriptPath(parentPath, agentID string) (string, error) {
	if parentPath == "" {
		return "", fmt.Errorf("transcript: sub-agent transcript path: %w", ErrNoTranscriptPath)
	}
	if err := checkAgentID(agentID); err != nil {
		return "", err
	}
	base := strings.TrimSuffix(parentPath, ".jsonl")
	return filepath.Join(base, SubagentDir, subagentFilePrefix+agentID+".jsonl"), nil
}

// checkAgentID refuses an agent id that could traverse out of the directory it
// is joined into. See SubagentTranscriptPath on why this refuses rather than
// sanitises.
func checkAgentID(agentID string) error {
	if agentID == "" {
		return fmt.Errorf("transcript: sub-agent transcript path: %w: it is empty", ErrNotAnAgentID)
	}
	if strings.ContainsAny(agentID, `/\`) || agentID == "." || agentID == ".." {
		return fmt.Errorf("transcript: sub-agent transcript path: %w: %q", ErrNotAnAgentID, agentID)
	}
	return nil
}
