package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/sessionstate"
)

// newSessionAgentsCmd is `sr-session agents list`: the session's sub-agent registry, as the Stop
// reads it. Read-only.
func newSessionAgentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "The sub-agents this session dispatched: list",
		Long: `The sub-agents this session dispatched, and whether each still runs.

  sr-session agents list [--json]    the registry: status, times, transcript, the ranges each owns

The registry is kept in the session's own store, fed by the SubagentStart / SubagentStop hooks, a
sub-agent's own tool calls and the dispatching transcript's task-notifications, and it survives a
compaction, /clear and resume. A background agent that is running has its ranges left for later at
the root's Stop; one silent for subagent_silent_after_minutes (default 10) is named there, and
after subagent_stale_after_minutes (default 60) it is stale and its ranges are judged.`,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "The session's sub-agent registry",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := openRefsSession(cmd)
			if err != nil {
				return err
			}
			defer s.reg.Close()
			agents, err := s.reg.Agents(s.rs.ID)
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(agentRows(agents))
			}
			for _, r := range agentRows(agents) {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  background=%t  ranges=%d\n", r.AgentID, r.Status, r.Background, len(r.Ranges))
			}
			return nil
		},
	})
	cmd.Commands()[0].Flags().Bool("json", false, "Print the registry as JSON")
	return cmd
}

type agentRow struct {
	AgentID        string   `json:"agent_id"`
	Status         string   `json:"status"`
	AgentType      string   `json:"agent_type,omitempty"`
	Background     bool     `json:"background"`
	StartedAt      string   `json:"started_at,omitempty"`
	EndedAt        string   `json:"ended_at,omitempty"`
	LastSeenAt     string   `json:"last_seen_at,omitempty"`
	TranscriptPath string   `json:"transcript_path,omitempty"`
	Ranges         []string `json:"ranges"`
}

func agentRows(agents []sessionstate.Agent) []agentRow {
	rows := make([]agentRow, 0, len(agents))
	for _, a := range agents {
		r := agentRow{AgentID: a.AgentID, Status: a.Status, AgentType: a.AgentType, Background: a.Background,
			StartedAt: rfc(a.StartedAt), EndedAt: rfc(a.EndedAt), LastSeenAt: rfc(a.LastSeenAt), TranscriptPath: a.TranscriptPath, Ranges: []string{}}
		for _, rg := range a.Ranges {
			r.Ranges = append(r.Ranges, rg.Folder+" "+rg.Head)
		}
		rows = append(rows, r)
	}
	return rows
}

func rfc(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
