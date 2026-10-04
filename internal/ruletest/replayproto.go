package ruletest

// The wire between `sr-checks test` and `sr-session replay`: one request on stdin,
// one response on stdout, per process, exactly as one hook invocation is one process.
// `replay` is hidden and acts only inside a sandbox (MarkerFile); it is the engine's
// real hook code run on normalized events instead of a harness's payloads.

// Replay operations.
const (
	// OpStart is the SessionStart hook: the session's baseline is taken.
	OpStart = "start"
	// OpEvent dispatches one Pre event, or ends the cycle with Kind "Stop".
	OpEvent = "event"
	// OpSubagentStart and OpSubagentStop are the sub-agent's own hooks.
	OpSubagentStart = "subagent-start"
	OpSubagentStop  = "subagent-stop"
	// OpContexts reads the contexts' states.
	OpContexts = "contexts"
)

// ReplayEvent is one normalized event: a kind and its fields as the case wrote them.
type ReplayEvent struct {
	Kind   string         `json:"kind"`
	Fields map[string]any `json:"fields,omitempty"`
}

// ReplayRequest is what the runner asks of one replay process.
type ReplayRequest struct {
	Op string `json:"op"`
	// Event is the event OpEvent dispatches.
	Event ReplayEvent `json:"event"`
	// Agent is the sub-agent the call comes from (OpEvent, OpSubagentStart/Stop).
	Agent string `json:"agent,omitempty"`
	// Post are the Post events delivered at this Stop: what the cycle changed or
	// tagged, in the order the trajectory gave them.
	Post []ReplayEvent `json:"post,omitempty"`
	// Judges are the case's canned judges; Live leaves them unstubbed.
	Judges *JudgeTable `json:"judges,omitempty"`
	Live   bool        `json:"live,omitempty"`
}

// ContextState is one context's `{active, payload}`.
type ContextState struct {
	Active  bool           `json:"active"`
	Payload map[string]any `json:"payload,omitempty"`
}

// ReplayResponse is what a replay process answers.
type ReplayResponse struct {
	// Refused is true when the hook denied the call or blocked the turn; Reason is
	// what the agent would have been told.
	Refused bool   `json:"refused"`
	Reason  string `json:"reason,omitempty"`
	// Stderr is what the hook wrote to its own stderr (diagnostics, load faults).
	Stderr string `json:"stderr,omitempty"`
	// Contexts answers OpContexts.
	Contexts map[string]ContextState `json:"contexts,omitempty"`
	// JudgeCalls are the judges the hook asked.
	JudgeCalls []JudgeCall `json:"judgeCalls,omitempty"`
	// Error is a failure of the replay itself (a malformed event, a sandbox that is
	// not one): the case fails, it is never a verdict.
	Error string `json:"error,omitempty"`
}
