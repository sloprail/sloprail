package e2e

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
)

// T023_03: a store whose registry kept every worktree and scratch repository the session ever
// touched (an older engine never forgot one) is pruned by the engine at the next hook: what is gone
// is dropped, what is still owed stays — the live folder, a tracked range of a folder that is gone,
// what was observed of a branch that exists.
func TestT023_03_ARegistryThatKeptEverythingIsPrunedAtTheNextHook(t *testing.T) {
	e, proj := citedProject(t)
	const sess = "s-023-03"
	e.Run(proj, sess, "start", Turns("done"))
	id := e.SessionIdentity(proj, sess)
	dbPath := e.StateDBPath(proj, sess)

	removed := func(i int) string { return filepath.Join(t.TempDir(), fmt.Sprintf("removed-%d", i)) }
	store, err := sessionstate.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		gone := removed(i)
		if _, err := store.RegisterFolder(sessionstate.Folder{SessionID: id, Path: gone, Role: sessionstate.FolderAdHoc, GitRoot: gone, AgentID: fmt.Sprintf("a%d", i)}); err != nil {
			t.Fatal(err)
		}
		if err := store.UntrackRange(id, gone, "wt", "finished", fmt.Sprintf("a%d", i), "abc"); err != nil {
			t.Fatal(err)
		}
		if err := store.SetMeta("observed-tip:wt:"+gone, "abc"); err != nil {
			t.Fatal(err)
		}
	}
	keptGone := removed(1000)
	if err := store.TrackRange(sessionstate.TrackedRange{SessionID: id, Folder: keptGone, Head: "feature", HeadSHA: "abc", Base: "b", AddedBy: sessionstate.RangeAgent}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMeta("observed-tip:main:"+proj, "abc"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMeta("refs_snapshot:"+proj, "legacy"); err != nil {
		t.Fatal(err)
	}
	foldersBefore, _ := store.Folders(id)
	if len(foldersBefore) < 300 {
		t.Fatalf("the fixture registry is not fat: %d folders", len(foldersBefore))
	}
	store.Close()
	old, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec("PRAGMA user_version = 9"); err != nil {
		t.Fatal(err)
	}
	old.Close()

	payload, _ := json.Marshal(map[string]any{
		"session_id": sess, "transcript_path": e.TranscriptPath(proj, sess), "cwd": proj,
		"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": "ls"},
	})
	if res := e.CLIDirectStdinEnv(proj, string(payload), e.SessionEnv(""), "sr-session", "pre-tool"); res.Code != 0 {
		t.Fatalf("the hook failed on an older store (exit %d):\n%s", res.Code, res.Output)
	}

	store, err = sessionstate.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	folders, _ := store.Folders(id)
	if len(folders) > 5 {
		t.Errorf("%d folders remain after the migration; the 300 that are gone must not", len(folders))
	}
	ranges, _ := store.Ranges(id)
	var tracked bool
	for _, r := range ranges {
		if r.Folder == keptGone && r.Tracked() {
			tracked = true
		} else if r.Folder != proj && !r.Tracked() && !filepath.IsAbs(r.Folder) {
			t.Errorf("unexpected row %+v", r)
		}
		if !r.Tracked() && r.Folder != proj && len(ranges) > 50 {
			t.Errorf("an untracked range of a gone folder survived: %+v", r)
		}
	}
	if !tracked {
		t.Errorf("the tracked range of a folder that is gone was dropped: it is still verified at its commit")
	}
	if v, ok, _ := store.Meta("observed-tip:main:" + proj); !ok || v != "abc" {
		t.Errorf("what was observed of a live branch was lost: %q %v", v, ok)
	}
	if _, ok, _ := store.Meta("refs_snapshot:" + proj); ok {
		t.Errorf("a key no code reads survived")
	}
}
