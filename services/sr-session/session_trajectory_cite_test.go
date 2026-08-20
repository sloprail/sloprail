package main

import "testing"

// failClosedNoPath is the no-path half of cite's sub-agent guard: it fires on the
// one resolution that yields a usable path yet cannot exclude a sub-agent caller —
// a trajectory resolved from a SESSION ID ALONE, which names the root even when a
// sub-agent is asking. These cases pin exactly where that line falls, because the
// whole point of the predicate is to be NARROW: it must refuse the unruleable
// session-id case without breaking the hook-invoked default that resolves a
// transcript_path.
//
// The grounding for why a session id cannot exclude a sub-agent is in the CC docs
// cited at the guard: agent identity reaches a process only on a hook's JSON stdin
// (agent_id / agent_type), never as an environment variable to a tool call, and no
// CLAUDE_SESSION_ID is exported to a tool call at all — so a session-id-derived path
// is the root's whoever is asking. [code.claude.com/docs/en/hooks.md;
// code.claude.com/docs/en/sessions.md]

func TestFailClosedNoPath(t *testing.T) {
	cases := []struct {
		name string
		p    HookPayload
		want bool
	}{
		{
			// The unruleable case: only a session id resolved the trajectory, and
			// record() builds <project>/<session-id>.jsonl from it — always the
			// ROOT's file, even when a sub-agent is the caller. cite must fail closed.
			name: "session id alone fails closed",
			p:    HookPayload{SessionID: "s-1", Cwd: "/proj"},
			want: true,
		},
		{
			// A hook payload naming a transcript_path is self-disambiguating: a
			// sub-agent's hook always ALSO carries an agent field, so a bare
			// transcript_path is genuinely the root's and is safe to cite. Not
			// fail-closed — this is the T029_06 default that must keep working.
			name: "transcript_path present does not fail closed",
			p:    HookPayload{TranscriptPath: "/cfg/projects/-proj/s.jsonl", SessionID: "s-1", Cwd: "/proj"},
			want: false,
		},
		{
			// A sub-agent's own path was reported: record() resolves the sub-agent
			// transcript and the PATH-based guard (IsSubagentTranscript) catches it,
			// so this predicate need not — and must not double-refuse via a different
			// code path. Not fail-closed here.
			name: "agent_transcript_path present is handled by the path guard",
			p:    HookPayload{AgentTranscriptPath: "/cfg/projects/-proj/s/subagents/agent-a.jsonl", SessionID: "s-1"},
			want: false,
		},
		{
			// A sub-agent named by id only: record() reconstructs the sub-agent's own
			// path from the parent's, which the path guard then catches. Again the
			// path guard's job, not this predicate's.
			name: "agent_id present is handled by the path guard",
			p:    HookPayload{AgentID: "a1", TranscriptPath: "/cfg/projects/-proj/parent.jsonl"},
			want: false,
		},
		{
			// An empty payload — the plain agent tool call with no --path and no piped
			// hook payload. Nothing resolves at all; record() returns "" and cite
			// refuses with errNoTrajectory. That is already fail-closed, so this
			// predicate leaves it to the empty-path branch rather than claiming the
			// session-id reason it does not have.
			name: "empty payload is left to the no-trajectory refusal",
			p:    HookPayload{},
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := failClosedNoPath(c.p); got != c.want {
				t.Fatalf("failClosedNoPath(%+v) = %v, want %v", c.p, got, c.want)
			}
		})
	}
}
