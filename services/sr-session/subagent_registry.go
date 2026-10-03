package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// The sub-agent registry: which sub-agents the session dispatched, and whether each still runs,
// kept in the session's own store (state.db, session_agents) rather than re-read from the
// dispatching transcript, so a compaction, a /clear or a resume into a new transcript file forgets
// nothing. It is fed by every signal sloprail sees:
//
//   - SubagentStart / SubagentStop hooks (subagent-start, subagent-stop);
//   - the agent's own tool calls (pre-tool with an agent id: last_seen_at, and "it is alive");
//   - the dispatching transcript's records (transcript.BackgroundAgentSignals): a background
//     launch and a terminal <task-notification>, read at the root's Stop.
//
// Status changes on a signal, never on a clock. The clock only decides what a Stop SAYS and when it
// stops WAITING for an agent that has gone silent: a display and escalation policy. CI verify is
// the guarantee whatever the registry holds. Fail closed throughout: only a background agent the
// registry holds as running has its ranges left unjudged; an agent it does not know, a foreground
// one, a stale one, one whose process is gone, is judged.

// agentClock and agentHome are variables so tests stand in the time and the harness's home.
var (
	agentClock = time.Now
	agentHome  = func() string { h, _ := os.UserHomeDir(); return h }
)

// agentSignalFor builds the registry's signal for a hook payload of a sub-agent.
func agentSignalFor(rsID string, p HookPayload, now time.Time) sessionstate.AgentSignal {
	sig := sessionstate.AgentSignal{SessionID: rsID, AgentID: p.AgentID, TranscriptPath: agentRecordPath(p), At: now}
	if proc, ok := harness.ProcessOfSession(agentHome(), p.SessionID); ok {
		sig.OwnerPID, sig.OwnerProcStart = proc.PID, proc.ProcStart
	}
	return sig
}

// agentRecordPath is the sub-agent's own transcript: reported on its payload, or reconstructed
// from the dispatching one's. "" when neither is known.
func agentRecordPath(p HookPayload) string {
	if p.AgentTranscriptPath != "" {
		return p.AgentTranscriptPath
	}
	if p.AgentID == "" || p.TranscriptPath == "" {
		return ""
	}
	path, err := transcript.SubagentTranscriptPath(p.TranscriptPath, p.AgentID)
	if err != nil {
		return ""
	}
	return path
}

// recordAgentSignal runs fn against the root session's registry for a sub-agent's hook. It never
// refuses anything: the registry failing is reported on stderr, and the agent's work goes on (the
// ranges of an agent the registry does not know are judged).
func recordAgentSignal(cmd *cobra.Command, p HookPayload, fn func(reg sessionstate.Store, rsID string, now time.Time) error) {
	if !p.IsSubagent() || p.AgentID == "" {
		return
	}
	rs, err := resolveRootSession(p)
	if err == nil {
		var reg sessionstate.Store
		if reg, err = sessionstate.Open(rs.Path); err == nil {
			defer reg.Close()
			err = fn(reg, rs.ID, agentClock())
		}
	}
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: sub-agent registry not updated:", err)
	}
}

// newSessionSubagentStartCmd is the hook point that fires when a sub-agent begins: it enters the
// registry as running. It records and never blocks; its stdout is not read by anything.
func newSessionSubagentStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "subagent-start",
		Short: "A SUB-AGENT is starting: record it in the session's sub-agent registry",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := readPayload(cmd)
			recordAgentSignal(cmd, p, func(reg sessionstate.Store, id string, now time.Time) error {
				sig := agentSignalFor(id, p, now)
				sig.AgentType = p.AgentType
				return reg.StartAgent(sig)
			})
			return nil
		},
	}
}

// touchAgent records a sub-agent's own tool call: it is alive.
func touchAgent(cmd *cobra.Command, p HookPayload) {
	recordAgentSignal(cmd, p, func(reg sessionstate.Store, id string, now time.Time) error {
		return reg.TouchAgent(agentSignalFor(id, p, now))
	})
}

// endAgent records that a sub-agent's Stop let it finish, or that it did not: a Stop that
// blocked sends the agent round again, so it is alive.
func endAgent(cmd *cobra.Command, p HookPayload, blocked bool) {
	recordAgentSignal(cmd, p, func(reg sessionstate.Store, id string, now time.Time) error {
		sig := agentSignalFor(id, p, now)
		if blocked {
			return reg.TouchAgent(sig)
		}
		if err := reg.TouchAgent(sig); err != nil { // makes an agent never seen before known, with its record
			return err
		}
		return reg.EndAgent(id, p.AgentID, sessionstate.AgentCompleted, now)
	})
}

// runCapturingBlock runs fn and reports whether it wrote a block decision to stdout, passing the
// output through unchanged.
func runCapturingBlock(cmd *cobra.Command, fn func() error) (blocked bool, err error) {
	var buf bytes.Buffer
	out := cmd.OutOrStdout()
	cmd.SetOut(&buf)
	err = fn()
	cmd.SetOut(out)
	_, _ = out.Write(buf.Bytes())
	return err != nil || strings.Contains(buf.String(), `"decision":"block"`), err
}

// agentPlan is what the root's Stop reads from the registry: the agents whose ranges it leaves
// unjudged for now, and what to say about them.
type agentPlan struct {
	// Waiting are the running background agents; their ranges are not judged at this Stop.
	Waiting map[string]bool
	// Silent are the notices for waiting agents that went quiet past the silent threshold.
	Silent map[string]string
}

// settleAgents folds the signals available at the root's Stop into the registry and decides which
// agents are still waited for.
//
// 1. The dispatching transcript's launches and terminal notifications (one input among the
// registry's: a notification is applied once, by the record that carried it, so a transcript read
// again, or a new file repeating the old records, does not end a later run).
// 2. A running agent whose harness process is gone (another process than this hook's, no longer
// alive) is stale: nothing can still be running under it.
// 3. A running agent silent for the stale threshold is stale; for the silent threshold it is still
// waited for, but named. Silence is the latest of its own last hook, its start and the
// modification of its own transcript.
//
// A registry that cannot be read waits for no one.
func settleAgents(cmd *cobra.Command, root sessionstate.Store, sessionID string, p HookPayload, now time.Time) agentPlan {
	plan := agentPlan{Waiting: map[string]bool{}, Silent: map[string]string{}}
	silentAfter, staleAfter, err := declaration.SubagentThresholds(dotDir(p.Cwd))
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
	}
	home := agentHome()
	current, haveCurrent := harness.ProcessOfSession(home, p.SessionID)

	if p.TranscriptPath != "" {
		signals, err := transcript.BackgroundAgentSignals(p.TranscriptPath)
		if err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: dispatching record not read for sub-agents:", err)
		}
		for _, s := range signals {
			sig := sessionstate.AgentSignal{SessionID: sessionID, AgentID: s.AgentID, At: now}
			if haveCurrent {
				sig.OwnerPID, sig.OwnerProcStart = current.PID, current.ProcStart
			}
			switch s.Kind {
			case transcript.AgentLaunched:
				err = root.NoteAgentLaunch(sig)
			case transcript.AgentEnded:
				var fresh bool
				if fresh, err = root.NewAgentSignal(sessionID, s.Key); err == nil && fresh {
					err = root.EndAgent(sessionID, s.AgentID, s.Status, now)
				}
			}
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: sub-agent registry not updated:", err)
				return plan
			}
		}
	}

	agents, err := root.Agents(sessionID)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: sub-agent registry not read:", err)
		return plan
	}
	for _, a := range agents {
		if !a.Running() {
			continue
		}
		stale := false
		if a.OwnerPID != 0 && (!haveCurrent || current.PID != a.OwnerPID || current.ProcStart != a.OwnerProcStart) {
			if gone, known := harness.ProcessGone(home, harness.Process{PID: a.OwnerPID, ProcStart: a.OwnerProcStart}); known && gone {
				stale = true
			}
		}
		quiet := now.Sub(lastActivity(a))
		if quiet >= staleAfter {
			stale = true
		}
		if stale {
			if err := root.MarkAgentStale(sessionID, a.AgentID, now); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: sub-agent registry not updated:", err)
			}
			continue
		}
		if !a.Background {
			continue // a foreground or unconfirmed agent is not waited for
		}
		plan.Waiting[a.AgentID] = true
		if quiet >= silentAfter && len(a.Ranges) > 0 {
			plan.Silent[a.AgentID] = fmt.Sprintf("sub-agent %s has been silent for %d min; its ranges are still unjudged", a.AgentID, int(quiet.Minutes()))
		}
	}
	return plan
}

// lastActivity is the latest sign of life of an agent: its own last hook, its start, the last
// write to its own transcript. A time the registry never learned is zero, so an agent with no
// sign at all reads as silent for ever and is escalated rather than waited for.
func lastActivity(a sessionstate.Agent) time.Time {
	last := a.LastSeenAt
	if a.StartedAt.After(last) {
		last = a.StartedAt
	}
	if a.TranscriptPath != "" {
		if fi, err := os.Stat(a.TranscriptPath); err == nil && fi.ModTime().After(last) {
			last = fi.ModTime()
		}
	}
	return last
}
