package main

import (
	"os"

	"github.com/sloprail/sloprail/internal/sessionpath"
)

// AppName is the directory this tool keeps its own data under.
const AppName = sessionpath.AppName

// GuardrailEnv names the guardrail whose hook is running.
//
// Which guardrail is asking is never a parameter to `session state`. The engine
// ran the hook and knows, and it tells the hook by putting it here rather than
// in the argument vector, where a hook could write a different name and read a
// rule it was never told about — and then depend on when that rule ran.
const GuardrailEnv = "SR_GUARDRAIL"

// SessionEnv names the session a hook belongs to, for the same reason.
const SessionEnv = "SR_SESSION_ID"

// WorkspaceEnv is the tree the session is guarding. A hook runs with its own
// working directory set to the guardrail's folder, so the process's cwd is not
// the workspace and cannot stand in for it.
const WorkspaceEnv = "SR_WORKSPACE"

// TranscriptEnv is the path of the record this session is writing, for a rule
// that reads the trajectory rather than the pending call.
//
// SessionEnv cannot stand in for it: that is the STABLE id, the uuid of the
// conversation's origin record, deliberately not the harness's current id and
// therefore not the transcript's filename. Unset when the payload named no
// record — see transcriptEnv for why this one is omitted where the workspace is
// given a sentinel instead.
const TranscriptEnv = "SR_TRANSCRIPT"

// AgentEnv names the sub-agent a hook fired inside (the harness's agent_id),
// empty in the main session. internal/dispatch sets the same name for a
// nature's checks.
const AgentEnv = "SR_AGENT_ID"

// Where a session's data lives is internal/sessionpath's answer, shared with
// sr-checks so both find the same files. These are this package's names for it.
var (
	workspaceAnchor = sessionpath.WorkspaceAnchor
)

// hookStateCwd is the directory a guardrail's own hook keys the session's store by:
// the same one the engine used (HookPayload.stateCwd), found from the environment the
// engine set. SR_WORKSPACE is the tree the hook was invoked in, which is not the
// session's start after the agent `cd`s; the record SR_TRANSCRIPT names says where
// the session began.
func hookStateCwd() string {
	return sessionpath.StateCwd(os.Getenv(TranscriptEnv), os.Getenv(WorkspaceEnv))
}

// sessionDBPath resolves where one session's state lives — see
// sessionpath.StateDB for the scheme and why a session id is refused when it is
// not a name.
//
// The one thing added here is the hook-only case: a hook whose engine could not
// say which tree it guards reaches for state under a sentinel workspace, and the
// fallback to the process's own directory must not be reached — a hook's process
// directory is the guardrail's own folder, so falling back would key this rule's
// state by where its scripts live and hand every rule a private database.
func sessionDBPath(cwd, sessionID string) (string, error) {
	if cwd == unresolvedWorkspace {
		return "", errUnresolvedWorkspace()
	}
	return sessionpath.StateDB(cwd, sessionID)
}
