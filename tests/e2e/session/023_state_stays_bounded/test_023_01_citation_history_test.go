package e2e

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// What a session remembers about citations must not grow with the session's age: a hook reads
// and rewrites it, so its size is every later hook's cost. One real session reached 988 MB and the
// first hook of a turn took minutes.
//
// The rule is a file-guard that requires a citation on memories/**: the one kind that makes the
// engine keep, per file, a history of how it changed between turns.
const citedGuard = `match: "memories/**"
require:
  - citation: {source_types: [user]}
checks:
  - script: ./ok.sh
`

const okScript = "#!/usr/bin/env bash\ncat >/dev/null\nexit 0\n"

type storedPoint struct {
	BetweenTurns bool   `json:"betweenTurns"`
	Foreign      bool   `json:"foreign"`
	By           string `json:"by"`
	Cites        []any  `json:"cites"`
	At           int64  `json:"at"`
}

func citationsOf(t *testing.T, e *harness.Env, proj, sess string) (map[string][]storedPoint, string) {
	t.Helper()
	raw := e.Meta(proj, sess, "citations")
	var h map[string][]storedPoint
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &h); err != nil {
			t.Fatalf("the citation history does not parse: %v", err)
		}
	}
	return h, raw
}

// stopWith runs a Stop the way the harness does, reporting the background work it says still runs.
func stopWith(t *testing.T, e *harness.Env, proj, sess string, running string) harness.Result {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"session_id": sess, "transcript_path": e.TranscriptPath(proj, sess), "cwd": proj,
		"hook_event_name": "Stop", "stop_hook_active": false,
		"background_tasks": []map[string]any{{"id": "t1", "type": "shell", "status": "running", "command": running}},
	})
	return e.CLIDirectStdinEnv(proj, string(payload), e.SessionEnv(""), "sr-session", "stop")
}

func citedProject(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "grounded-memories", citedGuard, map[string]string{"ok.sh": okScript})
	e.WriteFile(proj, "memories/a.md", "v0\n")
	e.CommitAll(proj, "baseline")
	return e, proj
}

// T023_01: a file a background job keeps rewriting between turns, over many turns, leaves the
// history bounded: the newest stretches, and a name for the work that may still run, cut short.
func TestT023_01_ManyTurnsKeepTheCitationHistoryBounded(t *testing.T) {
	e, proj := citedProject(t)
	const sess = "s-023-01"
	e.Run(proj, sess, "start", Turns("done"))

	script := strings.Repeat("sleep 1000 && echo working && ", 300) // a 9 KB command line
	for i := 1; i <= 90; i++ {
		e.WriteFile(proj, "memories/a.md", fmt.Sprintf("v%d\n", i))
		stopWith(t, e, proj, sess, fmt.Sprintf("nohup ./job-%d.sh %s", i, script))
	}

	h, raw := citationsOf(t, e, proj, sess)
	pts := h["memories/a.md"]
	if len(pts) == 0 {
		t.Fatalf("the engine recorded nothing of a file that changed between turns: %s", raw)
	}
	if len(pts) > sessionstate.MaxUncitedPoints {
		t.Errorf("the history of one file holds %d points after 90 turns, more than its bound of %d", len(pts), sessionstate.MaxUncitedPoints)
	}
	if len(raw) > 50_000 {
		t.Errorf("the history is %d bytes after 90 turns; it must stay the size of the work in the tree", len(raw))
	}
	for _, p := range pts {
		if len(p.By) > (sessionstate.MaxByEntryLen+8)*sessionstate.MaxByEntries {
			t.Errorf("a point names %d bytes of the work that may still run", len(p.By))
		}
	}
	cycle := e.Meta(proj, sess, "cited_cycle")
	if len(cycle) > 5_000 {
		t.Errorf("the cycle record is %d bytes", len(cycle))
	}
}

// T023_02: a store written by an older engine whose history grew to tens of megabytes is
// compacted by the engine itself at the next hook: the hook still answers, the history keeps what is
// owed (a cited change) and drops the repetition, and the file gives its space back.
func TestT023_02_AFatStoreIsCompactedAtTheNextHook(t *testing.T) {
	e, proj := citedProject(t)
	const sess = "s-023-02"
	e.Run(proj, sess, "start", Turns("done"))
	dbPath := e.StateDBPath(proj, sess)

	long := strings.Repeat("n", 40000)
	repeated := func(at int) map[string]any {
		return map[string]any{"betweenTurns": true, "by": "nohup " + long, "at": at, "fromAt": at - 1,
			"from": map[string]any{"exists": true, "hash": "eb4d"}, "before": map[string]any{"exists": false}, "after": map[string]any{"exists": true, "hash": "9ced"}}
	}
	owed := map[string]any{"cites": []map[string]any{{"quote": "adopt a decision log", "path": "/r.jsonl", "line": 1}},
		"before": map[string]any{"exists": true, "hash": "b"}, "after": map[string]any{"exists": true, "hash": "a"}, "at": 999999}
	history := map[string][]any{}
	for _, p := range []string{"memories/a.md", "memories/b.md", "memories/c.md"} {
		for i := 1; i <= 250; i++ {
			history[p] = append(history[p], repeated(i))
		}
	}
	history["memories/a.md"] = append(history["memories/a.md"], owed)
	raw, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}

	store, err := sessionstate.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetMeta("citations", string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// An older engine's store: the version before the bound existed.
	old, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec("PRAGMA user_version = 8"); err != nil {
		t.Fatal(err)
	}
	old.Close()
	if st, _ := os.Stat(dbPath); st == nil || st.Size() < 25_000_000 {
		t.Fatalf("the fixture store is not fat: %v", st)
	}

	payload, _ := json.Marshal(map[string]any{
		"session_id": sess, "transcript_path": e.TranscriptPath(proj, sess), "cwd": proj,
		"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": "ls"},
	})
	started := time.Now()
	res := e.CLIDirectStdinEnv(proj, string(payload), e.SessionEnv(""), "sr-session", "pre-tool")
	took := time.Since(started)
	if res.Code != 0 {
		t.Fatalf("the hook failed on a store from an older engine (exit %d):\n%s", res.Code, res.Output)
	}
	if took > 30*time.Second {
		t.Errorf("the hook took %s: the migration must not make it unusable", took)
	}

	h, got := citationsOf(t, e, proj, sess)
	if len(got) > 10_000 {
		t.Errorf("the history is still %d bytes after the migration", len(got))
	}
	var sawOwed bool
	for _, p := range h["memories/a.md"] {
		sawOwed = sawOwed || (len(p.Cites) == 1 && p.At == 999999)
	}
	if !sawOwed {
		t.Errorf("the cited change was lost by the migration: %s", got)
	}
	for _, p := range []string{"memories/b.md", "memories/c.md"} {
		if len(h[p]) != 1 {
			t.Errorf("%s: the repeated stretch was not folded into one: %d points", p, len(h[p]))
		}
	}
	if st, _ := os.Stat(dbPath); st == nil || st.Size() > 5_000_000 {
		t.Errorf("the store still occupies %v bytes", st.Size())
	}
}
