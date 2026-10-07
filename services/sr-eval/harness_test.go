package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sloprail/sloprail/internal/harness"
)

func testHarness(t *testing.T, id string) harness.Harness {
	t.Helper()
	h, ok := harness.Lookup(id)
	if !ok {
		t.Fatalf("harness %q is not registered", id)
	}
	return h
}

// The harness under test is named by the flag, else SLOPRAIL_HARNESS, else Claude Code,
// and only by those: never by the operator's own session.
func TestResolveHarness_FlagThenEnvThenClaude(t *testing.T) {
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	for _, c := range []struct {
		flag string
		env  map[string]string
		want string
	}{
		{"", nil, "claude"},
		{"", map[string]string{"SLOPRAIL_HARNESS": "codex"}, "codex"},
		{"cursor", map[string]string{"SLOPRAIL_HARNESS": "codex"}, "cursor"},
		{"", map[string]string{"CLAUDECODE": "1", "CURSOR_AGENT": "1"}, "claude"},
	} {
		h, err := resolveHarness(c.flag, env(c.env))
		if err != nil || h.Name() != c.want {
			t.Errorf("flag %q env %v: got %v, %v; want %s", c.flag, c.env, h, err, c.want)
		}
	}
	if _, err := resolveHarness("claude-code", env(nil)); err == nil {
		t.Error("an id that is not claude, codex or cursor must be refused")
	}
}

// Every harness is provisionable, and what a provisioner says is consistent: a binary,
// a config dir under the sandbox's HOME, and auth files that stay inside it.
func TestProvisioners_AreCompleteForEveryHarness(t *testing.T) {
	for _, id := range []string{"claude", "codex", "cursor"} {
		p, err := harness.ProvisionerOf(testHarness(t, id))
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if p.Binary() == "" {
			t.Errorf("%s: no binary", id)
		}
		if got := p.ConfigDirIn("/h"); filepath.Dir(got) != "/h" {
			t.Errorf("%s: config dir %q is not directly under HOME", id, got)
		}
		for _, f := range p.AuthFiles() {
			if filepath.IsAbs(f) || filepath.Ext(f) == "" {
				t.Errorf("%s: auth file %q must be a HOME-relative file, never a directory", id, f)
			}
		}
	}
}

func TestFixtureSupports(t *testing.T) {
	all := Fixture{}
	only := Fixture{Harnesses: []string{"claude", "codex"}}
	if !all.Supports("cursor") || !only.Supports("codex") || only.Supports("cursor") {
		t.Fatal("an empty list is every harness; a list is exactly those")
	}
}

func TestLoadFixture_HarnessesMustBeKnownIDs(t *testing.T) {
	dir := newTestFixtureTree(t, false)
	mustWriteFile(t, filepath.Join(dir, "fixture.yaml"), "seed: seed\nmodel: haiku\nscore: score.sh\nharnesses: [claude, cursor]\n")
	fx, err := LoadFixture(dir)
	if err != nil || len(fx.Harnesses) != 2 {
		t.Fatalf("known ids must load: %v, %v", fx.Harnesses, err)
	}
	mustWriteFile(t, filepath.Join(dir, "fixture.yaml"), "seed: seed\nmodel: haiku\nscore: score.sh\nharnesses: [claude-code]\n")
	if _, err := LoadFixture(dir); err == nil {
		t.Fatal("an unknown harness id must be a load error")
	}
}

// findTranscript reads each harness's own layout and prefers a session's own record
// over its sub-agents' (deeper), whatever their mtimes.
func TestFindTranscript_PerHarnessLayoutShallowestWins(t *testing.T) {
	project := "/ws/project"
	touch := func(path string, age time.Duration) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		mustWriteFile(t, path, "{}\n")
		at := time.Now().Add(-age)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		id   string
		main string // relative to the harness's project dir
		sub  string
	}{
		{"claude", "sid.jsonl", "sid/subagents/agent-1.jsonl"},
		{"cursor", "agent-transcripts/c1/c1.jsonl", "agent-transcripts/c1/subagents/s.jsonl"},
		{"codex", "2026/10/07/rollout-t-id.jsonl", ""},
	} {
		tr := testHarness(t, c.id).Transcripts()
		config := t.TempDir()
		root := tr.ProjectDir(config, project)
		touch(filepath.Join(root, c.main), time.Hour)
		if c.sub != "" {
			touch(filepath.Join(root, c.sub), 0) // newer, but a sub-agent's
		}
		if got := findTranscript(tr, project, config); got != filepath.Join(root, c.main) {
			t.Errorf("%s: got %q, want %q", c.id, got, filepath.Join(root, c.main))
		}
	}
	tr := testHarness(t, "claude").Transcripts()
	if got := findTranscript(tr, project, t.TempDir()); got != "" {
		t.Errorf("no record must be \"\", got %q", got)
	}
}
