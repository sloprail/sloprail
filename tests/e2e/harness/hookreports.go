package harness

import (
	"encoding/json"
	"strings"
)

// HookReport is what one hook run printed, as the session's record shows it.
type HookReport struct {
	// Event is the lifecycle event of the hook that printed it, "" where the record does
	// not say (Codex writes a hook's stdout as a developer message and names no event).
	Event string
	// Stdout is the text that reached the agent's context; Stderr what was recorded with
	// the run (Claude Code records both, Codex only the first).
	Stdout, Stderr string
}

// hookReporter is what a Driver implements when its record does not keep hook output as
// Claude Code's attachments (the default): every hook run's report, in order.
type hookReporter interface {
	HookReports(record string) []HookReport
}

// HookReports are the hook runs of a session whose output contains needle, in order: one
// per hook run that printed it.
func (e *Env) HookReports(projDir, sessionID, needle string) []HookReport {
	e.t.Helper()
	record := e.transcript(projDir, sessionID)
	var all []HookReport
	if r, ok := e.driver.(hookReporter); ok {
		all = r.HookReports(record)
	} else {
		all = claudeHookReports(record)
	}
	var out []HookReport
	for _, r := range all {
		if strings.Contains(r.Stdout+r.Stderr, needle) {
			out = append(out, r)
		}
	}
	return out
}

// claudeHookReports reads Claude Code's hook attachments: each hook that printed leaves one.
func claudeHookReports(record string) []HookReport {
	var out []HookReport
	for _, l := range strings.Split(record, "\n") {
		var rec struct {
			Attachment struct {
				HookEvent string `json:"hookEvent"`
				Stdout    string `json:"stdout"`
				Stderr    string `json:"stderr"`
			} `json:"attachment"`
		}
		if json.Unmarshal([]byte(l), &rec) != nil ||
			rec.Attachment.HookEvent == "" && rec.Attachment.Stdout == "" && rec.Attachment.Stderr == "" {
			continue
		}
		out = append(out, HookReport{Event: rec.Attachment.HookEvent, Stdout: rec.Attachment.Stdout, Stderr: rec.Attachment.Stderr})
	}
	return out
}

// HookReports: a hook's stdout reaches a Codex agent as a developer message of the rollout.
func (codexDriver) HookReports(record string) []HookReport {
	var out []HookReport
	for _, l := range strings.Split(record, "\n") {
		var rec struct {
			Type    string `json:"type"`
			Payload struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(l), &rec) != nil || rec.Type != "response_item" ||
			rec.Payload.Type != "message" || rec.Payload.Role != "developer" {
			continue
		}
		for _, c := range rec.Payload.Content {
			out = append(out, HookReport{Stdout: c.Text})
		}
	}
	return out
}
