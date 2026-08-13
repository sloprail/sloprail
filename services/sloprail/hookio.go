package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/transcript"
)

// errNotASessionID: the reported session id is not a name, so no filename may
// be built from it. See record.
var errNotASessionID = errors.New("sloprail: the reported session id is not a session id")

// HookPayload is what a harness puts on a hook's standard input.
//
// A snapshot, not a contract: none of this is published or promised, and every
// field here was established by reading what harnesses actually send. Writing
// it down is what makes a change visible as a difference rather than as a
// guardrail that quietly stops firing.
type HookPayload struct {
	TranscriptPath string `json:"transcript_path"`

	// AgentTranscriptPath is the SUB-AGENT's own record, present when this hook
	// is a sub-agent's rather than the dispatching session's. The harness reports
	// it alongside the parent's path rather than instead of it, so a hook reading
	// TranscriptPath alone silently answers for the parent — see record().
	AgentTranscriptPath string `json:"agent_transcript_path"`

	// AgentID names the sub-agent within the session that dispatched it. Kept
	// because it is how the sub-agent's own record is found when a harness
	// reports the sub-agent without reporting where it wrote it.
	AgentID string `json:"agent_id"`

	// SessionID is the id the harness currently reports. Never the identity
	// anything is keyed on — Claude Code re-forks it mid-conversation — but it
	// names the file the harness is writing, which is what makes it worth
	// keeping: it is the only way to find the record when the payload omits its
	// path, and SessionStart is precisely where that happens.
	SessionID string `json:"session_id"`

	Cwd            string          `json:"cwd"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	StopHookActive bool            `json:"stop_hook_active"`
}

// record is the transcript whose session this hook belongs to.
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
// A session id carrying a path separator is refused rather than repaired.
// "../-other-project/secret" joined onto the project directory is cleaned by
// filepath.Join AFTER the concatenation, so the traversal lands in another
// project's directory and resolves that conversation's identity with no error at
// all. filepath.Base would make the path safe and the anomaly invisible. Both
// slashes are refused because Windows separates on both. "." and ".." need no
// clause: the suffix defuses them before the join, so "." becomes "..jsonl" and
// ".." becomes "...jsonl", ordinary filenames inside the project directory.
func (p HookPayload) record() (string, error) {
	if p.AgentTranscriptPath != "" {
		return p.AgentTranscriptPath, nil
	}
	if p.AgentID != "" && p.TranscriptPath != "" {
		return transcript.SubagentTranscriptPath(p.TranscriptPath, p.AgentID)
	}
	if p.TranscriptPath != "" {
		return p.TranscriptPath, nil
	}
	if p.SessionID == "" {
		// No path and no id: nothing to resolve and nothing to guess from. Not a
		// fault in itself — the caller decides whether it can proceed without one.
		return "", nil
	}
	if strings.ContainsAny(p.SessionID, `/\`) {
		return "", fmt.Errorf("%w: %q", errNotASessionID, p.SessionID)
	}
	dir := transcript.ProjectDir(transcript.ConfigDir(), p.Cwd)
	if dir == "" {
		return "", nil
	}
	return filepath.Join(dir, p.SessionID+".jsonl"), nil
}

// IsSubagent reports whether this payload belongs to a sub-agent rather than the
// session that dispatched it.
//
// Which agent is ending is never derived from a transcript's contents — the
// event that fired already said. Stop fires only in the root and SubagentStop
// only in a sub-agent, so the invocation carries the answer, and the two fields
// below are reported to a sub-agent's hook and to no other.
//
// Sniffing isSidechain instead would be worse than redundant, though not for
// the reason an earlier version of this comment gave. That version claimed a
// root transcript legitimately contains sidechain records, "every root that has
// ever dispatched anything does". That is measurably false: of the 8,119 main
// transcripts on one machine, ZERO contain a single sidechain record, including
// all 89 that demonstrably dispatched a sub-agent. Claude Code writes a
// sub-agent's records to the sub-agent's own file, not into its parent's — which
// is also why relating a root to its sub-agents needs the sibling subagents/
// directory, and why no scan of a root's own records could ever find one.
//
// The real objection is that it answers the question from the wrong kind of
// thing. Whose cycle this is was settled by the invocation — Stop fires only in
// a root, SubagentStop only in a sub-agent — so reading records to re-derive it
// replaces a fact with an inference, and an inference is only ever as good as
// the layout it was measured against. It would also be answering a question
// about THIS INVOCATION with evidence about a FILE: the same transcript is read
// by both a sub-agent's hook and, at the parent's own Stop, by the parent's, so
// no property of its contents can distinguish the caller. And it would rest on
// the absence measured above continuing to hold, which is a fact about the
// harness's current file layout rather than anything promised.
//
// That trap is why this package exposes no "is this file a sub-agent's" helper:
// the question is answered by the invocation, and offering a second way to
// answer it from evidence is offering a way to get it wrong.
//
// Used for reporting and for guarding, not for routing: record() already picks
// by the same fields, so nothing depends on this to find a transcript.
func (p HookPayload) IsSubagent() bool {
	return p.AgentTranscriptPath != "" || p.AgentID != ""
}

// Tool implements filemod.Pending: what the harness calls the tool it is about
// to run.
func (p HookPayload) Tool() string { return p.ToolName }

// Arguments implements filemod.Pending: the tool's own arguments, undecoded.
// Kept raw because what they mean is the tool's business and each module reads
// only what it recognises.
func (p HookPayload) Arguments() json.RawMessage { return p.ToolInput }

// readPayload reads the hook payload from stdin.
//
// An unreadable or empty body yields a zero payload rather than an error. A
// hook that cannot read its input has nothing to judge, and an engine that
// failed here would block work for a reason that has nothing to do with any
// rule the project declared.
func readPayload(cmd *cobra.Command) HookPayload {
	var p HookPayload
	b, err := io.ReadAll(cmd.InOrStdin())
	if err != nil || len(b) == 0 {
		return p
	}
	_ = json.Unmarshal(b, &p)
	return p
}

// deny refuses a pending tool call, in the shape this harness expects.
func deny(cmd *cobra.Command, reason string) error {
	return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": reason,
		},
	})
}

// block refuses to let a cycle end, in the shape this harness expects.
func block(cmd *cobra.Command, reason string) error {
	return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
		"decision": "block",
		"reason":   reason,
	})
}
