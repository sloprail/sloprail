package e2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

type registryRow struct {
	AgentID    string   `json:"agent_id"`
	Status     string   `json:"status"`
	Background bool     `json:"background"`
	LastSeenAt string   `json:"last_seen_at"`
	Ranges     []string `json:"ranges"`
}

func registryOf(t *testing.T, e *Env, proj, sess string) []registryRow {
	t.Helper()
	r := e.CLIDirectEnv(proj, e.SessionEnv(sess), "sr-session", "agents", "list", "--json")
	if r.Code != 0 {
		t.Fatalf("agents list: exit %d:\n%s", r.Code, r.Output)
	}
	var rows []registryRow
	if err := json.Unmarshal([]byte(r.Output), &rows); err != nil {
		t.Fatalf("agents list --json is not JSON (%v):\n%s", err, r.Output)
	}
	return rows
}

// T003_78: the sub-agent registry is fed by the hooks the plugin registers. A sub-agent the root
// dispatched is in the session's registry once its run ended (SubagentStart opened it,
// SubagentStop closed it), with the range it owns, and a later turn, even one that compacts the
// conversation, does not lose it.
func TestT003_78_ADispatchedSubagentIsInTheRegistryAcrossACompaction(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	const sess = "s-003-78"
	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("b1", "git switch -q -c sub-a"),
		harness.CommitFile("c1", "docs/a.md", "fine words", "sub adds a"),
	))
	e.Run(proj, sess, "delegate", Turns("root done", harness.Dispatch("d1", "write the docs", sub, "worktree")))
	rows := registryOf(t, e, proj, sess)
	if len(rows) != 1 {
		t.Fatalf("want the one dispatched sub-agent in the registry, got %+v", rows)
	}
	if rows[0].Status != "completed" || rows[0].Background {
		t.Fatalf("a foreground sub-agent that finished is completed and not background: %+v", rows[0])
	}
	if !strings.Contains(strings.Join(rows[0].Ranges, "\n"), " sub-a") {
		t.Fatalf("the registry names the range the agent owns (sub-a): %+v", rows[0])
	}
	id := rows[0].AgentID

	e.Run(proj, sess, "go on", Turns("again", harness.Compact("k1")))
	again := registryOf(t, e, proj, sess)
	if len(again) != 1 || again[0].AgentID != id || again[0].Status != "completed" {
		t.Fatalf("a compaction changed the registry: %+v", again)
	}
}

// T003_78b: a background sub-agent is marked background from the dispatching record, and the
// terminal notification that hands it back ends its run.
func TestT003_78_ABackgroundSubagentIsMarkedAndItsNotificationEndsIt(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	const sess = "s-003-78b"
	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("b1", "git switch -q -c sub-b"),
		harness.CommitFile("c1", "docs/b.md", "fine words", "sub adds b"),
	))
	e.Run(proj, sess, "delegate in the background", Turns("root done",
		harness.Background("ag1", "Agent", map[string]string{"prompt": "write the docs", "description": "background", "script": sub, "isolation": "worktree"}),
	))
	rows := registryOf(t, e, proj, sess)
	if len(rows) != 1 || !rows[0].Background {
		t.Fatalf("want one background sub-agent in the registry, got %+v", rows)
	}
	if rows[0].Status != "completed" {
		t.Fatalf("the notification that handed the agent back did not end its run: %+v", rows[0])
	}
}
