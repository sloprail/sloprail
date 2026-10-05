package main

import (
	"fmt"
	"os"
)

// hookScope is what a hook needs to find its own corner of the session's state:
// which session, which tree, and — filled in per hook — which guardrail.
//
// Resolved once per invocation and carried down, rather than re-derived where
// it is used. The session id in particular is not the one the harness reported
// but the one stableID works out from the transcript, and a second path
// deriving it differently would key the same conversation two ways.
type hookScope struct {
	// SessionID is the identity the conversation keeps, from stableID.
	SessionID string

	// Workspace is the tree being guarded. A hook runs with its working
	// directory set to the guardrail's own folder, so the process cwd is not
	// the workspace and cannot stand in for it.
	Workspace string

	// Transcript is the path of the record this hook's session is writing, from
	// HookPayload.record — so a sub-agent's hook is given the sub-agent's own
	// file, by the same choice everything else keys a session by.
	//
	// SessionID cannot stand in for it. That is the STABLE id, the uuid of the
	// conversation's origin record, deliberately not the harness's current id
	// and therefore not the transcript's filename; a path built from it lands on
	// a file that does not exist, which was measured rather than assumed. A rule
	// that reads the trajectory needs the record itself, so it is carried
	// separately.
	Transcript string

	// ToolUseID is the tool call a pre-tool hook decides on, for the decision log.
	ToolUseID string
}

// env is the environment to run one guardrail's hook in.
//
// The parent's environment is inherited, because a hook is an ordinary shell
// command with ordinary needs: PATH above all, since without it `sh -c` cannot
// find `sloprail` itself and every hook that calls back in would fail to run —
// and a hook that cannot run is a refusal, so a clean environment would turn
// every state-using rule into a rule that blocks all work. HOME, TMPDIR and the
// rest are wanted for the same reason: hooks run git, jq, and whatever else the
// project already depends on.
//
// Everything the engine sets is appended AFTER the inherited block, which is
// what makes it win. Go's exec resolves a repeated name to the last
// occurrence, so an SR_GUARDRAIL inherited from an outer process — a hook that
// itself ran sloprail, or a developer with one exported in their shell — cannot
// override the engine's answer about which rule is currently running. That
// ordering is the whole mechanism, and it is pinned by a test.
//
// SLOPRAIL_LAUNCHED_BY needs the identical property for a sharper reason: that
// value is inherited BY DESIGN — it is how a hook firing inside a launched agent
// learns which rules it is running underneath — so the engine must be the only
// thing that extends it. Appending it in this same block gets that for free,
// which is why it is assembled here rather than beside this function. One exec
// gets its environment from one place; two functions each appending to
// os.Environ() is how one silently drops the other's variable.
//
// The session is passed through even when it is empty, because empty is
// diagnosable: `session state` reports "no session id" and the hook's author
// learns what is missing. The workspace is NOT — see workspaceEnv — and that
// asymmetry is the whole reason it is built separately rather than appended
// alongside the others. The transcript is a third case again: see transcriptEnv.
func (s hookScope) env(guardrail string) []string {
	env := append(os.Environ(),
		GuardrailEnv+"="+guardrail,
		SessionEnv+"="+s.SessionID,
		// Which rules this process is running underneath. Inherited across the
		// exec into a launched agent's own hooks, which is what lets the engine
		// one level down decline to enforce the rule that launched it.
		LaunchedByEnv+"="+appendLaunchedBy(os.Getenv, guardrail),
	)
	env = append(env, s.workspaceEnv()...)
	return append(env, s.transcriptEnv()...)
}

// transcriptEnv is the record's path, or nothing at all.
//
// UNSET when there is no record, which is the opposite of the workspace's
// answer, and the difference is what each silence would mean downstream. An
// unset SR_WORKSPACE is read by sessionDBPath as "use the process's own
// directory" — a wrong answer that resolves — so it must be set to something
// that cannot resolve. Nothing reads SR_TRANSCRIPT except a rule that chose to,
// and a rule reading it asks a question that has no answer when no record
// exists: `test -n "$SR_TRANSCRIPT"` is the whole check, and it is one an
// example already performs by name.
//
// Set-but-empty would be the worse of the two here. It reads as a path in every
// shell idiom that does not test for emptiness first, so a rule would hand ""
// to `sr-session query` and get an error about the file rather than about
// the variable.
//
// An outer value surviving into the hook is not the risk it is for the other
// three. Those name the engine's own answer about the current invocation, which
// an inherited value could contradict; this names a file, and a stale path
// either belongs to a record that no longer describes this session — in which
// case the rule reading it is already asking about the wrong session by any
// route — or does not exist. The variable is left unset rather than blanked
// because a rule that tests for emptiness is the documented way to read it.
func (s hookScope) transcriptEnv() []string {
	if s.Transcript == "" {
		return nil
	}
	return []string{TranscriptEnv + "=" + s.Transcript}
}

// workspaceEnv is the workspace variable, or a refusal to set one.
//
// An empty workspace must not be handed over as an empty SR_WORKSPACE. Nothing
// downstream would report it: sessionDBPath treats an empty workspace as
// "use the process's own directory", and a hook's process directory is the
// GUARDRAIL'S OWN FOLDER — so every rule would silently get its own database,
// keyed by where its scripts happen to live rather than by the tree being
// guarded. Two rules watching one project would stop being able to see the same
// session, and nothing would say so.
//
// Leaving the variable UNSET is not the answer either, because then an
// SR_WORKSPACE exported by some outer process survives into the hook and the
// engine's silence becomes that stale value's permission to stand — the exact
// inheritance the append ordering exists to defeat.
//
// So the variable is set, and set to something that cannot be mistaken for a
// directory: sessionDBPath rejects it, the hook gets an error naming the
// variable, and the failure is loud instead of being one database per rule.
func (s hookScope) workspaceEnv() []string {
	if s.Workspace == "" {
		return []string{WorkspaceEnv + "=" + unresolvedWorkspace}
	}
	return []string{WorkspaceEnv + "=" + s.Workspace}
}

// unresolvedWorkspace is what SR_WORKSPACE says when the payload named no
// working directory.
//
// Deliberately not a path and deliberately not empty. Empty is the value
// sessionDBPath reads as "use the process's directory", which is the bug. A NUL
// byte would be the tidiest impossible value, but exec refuses to start a
// process whose environment contains one — the hook would fail to run at all,
// and a hook that cannot run is a refusal, so an unresolvable workspace would
// block every action instead of reporting itself. This is a value the hook's
// process really receives, that no filesystem answers to, and that
// sessionDBPath recognises by name.
const unresolvedWorkspace = "!sloprail:workspace-unresolved"

// errUnresolvedWorkspace is what a hook is told when it reaches for state under
// a workspace the engine could not resolve.
func errUnresolvedWorkspace() error {
	return fmt.Errorf(
		"sloprail: %s names no workspace — the hook payload carried no working directory, so there is no tree to key this session's state by",
		WorkspaceEnv)
}
