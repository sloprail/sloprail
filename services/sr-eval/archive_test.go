package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// archiveRun is the `sr-eval run` archive. These tests pin what it writes and commits — layout,
// bytes, modes, commit message and identity — and use only archiveRun's own contract, so the same
// file passes against the implementation from before archivekit.go was extracted (#221), which is how
// "the refactor changed nothing" was checked.

func archiveRunWorld(t *testing.T) (root, transcript string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "eval-runs")
	t.Setenv("SLOPRAIL_EVAL_RUNS_DIR", root)
	// A machine with no git identity and a signing default: the archive's fallback identity must
	// apply and the commit must not try to sign.
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, []byte("[commit]\n\tgpgsign = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_AUTHOR_NAME", "")
	t.Setenv("GIT_AUTHOR_EMAIL", "")
	t.Setenv("GIT_COMMITTER_NAME", "")
	t.Setenv("GIT_COMMITTER_EMAIL", "")
	os.Unsetenv("GIT_AUTHOR_NAME")
	os.Unsetenv("GIT_AUTHOR_EMAIL")
	os.Unsetenv("GIT_COMMITTER_NAME")
	os.Unsetenv("GIT_COMMITTER_EMAIL")
	t.Setenv("EMAIL", "")
	os.Unsetenv("EMAIL")

	proj := t.TempDir()
	transcript = filepath.Join(proj, "abc-123.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"type":"user","message":"hi"}`+"\n"+`{"type":"assistant"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	subs := filepath.Join(proj, "abc-123", "subagents")
	if err := os.MkdirAll(filepath.Join(subs, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subs, "agent-a.jsonl"), []byte("agent a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subs, "nested", "agent-b.jsonl"), []byte("agent b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, transcript
}

func archGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimRight(string(out), "\n")
}

func TestArchiveRun_WritesAndCommitsTheRun(t *testing.T) {
	root, transcript := archiveRunWorld(t)
	started := time.Date(2026, 10, 4, 9, 8, 7, 0, time.FixedZone("x", 3600))
	rec := runRecord{
		Fixture: "my-fixture", FixtureDir: "/abs/fixtures/my-fixture", Model: "sonnet", Harness: "claude",
		Passed: true, Transcript: transcript, StartedAt: started, FinishedAt: started.Add(time.Minute),
	}
	verdict := &Verdict{Subject: "s", Status: "pass", Rows: []VerdictRow{{CheckID: "c1", Status: "pass", Reasoning: "fine"}}}

	dir, err := archiveRun(rec, transcript, []byte("score out\n"), []byte("score err\n"), verdict)
	if err != nil {
		t.Fatal(err)
	}

	// <root>/<fixture>/<UTC timestamp>-<8 hex>
	if filepath.Dir(dir) != filepath.Join(root, "my-fixture") {
		t.Fatalf("run dir %s", dir)
	}
	if !regexp.MustCompile(`^20261004T080807Z-[0-9a-f]{8}$`).MatchString(filepath.Base(dir)) {
		t.Fatalf("run id %q", filepath.Base(dir))
	}

	rec.HasSubagents = true // set by archiveRun, since subagent transcripts were found
	runJSON, _ := json.MarshalIndent(rec, "", "  ")
	vJSON, _ := json.MarshalIndent(verdict, "", "  ")
	want := map[string]string{
		"transcript.jsonl":               readTestFile(t, transcript),
		"subagents/agent-a.jsonl":        "agent a\n",
		"subagents/nested/agent-b.jsonl": "agent b\n",
		"score/stdout.txt":               "score out\n",
		"score/stderr.txt":               "score err\n",
		"score/verdict.json":             string(vJSON),
		"run.json":                       string(runJSON),
	}
	got := map[string]string{}
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			got[filepath.ToSlash(rel)] = readTestFile(t, p)
		}
		return nil
	})
	if len(got) != len(want) {
		t.Fatalf("files: got %v, want %v", keys(got), keys(want))
	}
	for name, content := range want {
		if got[name] != content {
			t.Errorf("%s differs:\n%q\nwant\n%q", name, got[name], content)
		}
	}
	// modes: a copy keeps the source's permission bits; written files are 0644
	for name, perm := range map[string]os.FileMode{"subagents/nested/agent-b.jsonl": 0o600, "transcript.jsonl": 0o644, "run.json": 0o644, "score/verdict.json": 0o644} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != perm {
			t.Errorf("%s mode %v (%v), want %v", name, info.Mode().Perm(), err, perm)
		}
	}

	// exactly one commit: the run's files and nothing else, subject "<fixture>: pass (<model>)",
	// by the placeholder identity, unsigned
	if n := archGit(t, root, "rev-list", "--count", "HEAD"); n != "1" {
		t.Fatalf("commits: %s", n)
	}
	rel, _ := filepath.Rel(root, dir)
	var wantFiles []string
	for name := range want {
		wantFiles = append(wantFiles, filepath.ToSlash(filepath.Join(rel, name)))
	}
	sort.Strings(wantFiles)
	files := strings.Split(archGit(t, root, "show", "--name-only", "--pretty=format:", "HEAD"), "\n")
	sort.Strings(files)
	if strings.Join(files, "\n") != strings.Join(wantFiles, "\n") {
		t.Fatalf("committed files:\n%v\nwant\n%v", files, wantFiles)
	}
	if got := archGit(t, root, "log", "-1", "--format=%s|%an|%ae|%cn|%ce|%G?"); got != "my-fixture: pass (sonnet)|sr-eval|sr-eval@localhost|sr-eval|sr-eval@localhost|N" {
		t.Fatalf("commit: %q", got)
	}
	if st := archGit(t, root, "status", "--porcelain"); st != "" {
		t.Fatalf("not clean: %q", st)
	}
	// the transcript is copied, not moved
	if _, err := os.Stat(transcript); err != nil {
		t.Fatalf("source transcript gone: %v", err)
	}
}

func TestArchiveRun_FailureWithoutTranscriptOrVerdict(t *testing.T) {
	root, _ := archiveRunWorld(t)
	rec := runRecord{Fixture: "f", Model: "opus", Passed: false, Reason: "scorer said no", AgentError: "exit 1", StartedAt: time.Now(), FinishedAt: time.Now()}

	dir, err := archiveRun(rec, "", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"transcript.jsonl", "subagents", "score/verdict.json"} {
		if _, err := os.Stat(filepath.Join(dir, absent)); err == nil {
			t.Errorf("%s exists", absent)
		}
	}
	for _, empty := range []string{"score/stdout.txt", "score/stderr.txt"} {
		if b := readTestFile(t, filepath.Join(dir, empty)); b != "" {
			t.Errorf("%s = %q", empty, b)
		}
	}
	var back runRecord
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join(dir, "run.json"))), &back); err != nil || back.Reason != "scorer said no" || back.HasSubagents {
		t.Fatalf("run.json: %+v (%v)", back, err)
	}
	if got := archGit(t, root, "log", "-1", "--format=%s"); got != "f: fail (opus)" {
		t.Fatalf("subject %q", got)
	}
}

// A second run of the same fixture is committed apart: its commit holds its own directory only,
// even when something else is staged in the archive by hand, and an archive configured with its
// own identity commits as that identity.
func TestArchiveRun_CommitsOnlyTheRunsOwnDirectoryAsTheArchivesOwnIdentity(t *testing.T) {
	root, transcript := archiveRunWorld(t)
	rec := runRecord{Fixture: "f", Model: "m", Passed: true, StartedAt: time.Now()}
	first, err := archiveRun(rec, transcript, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	archGit(t, root, "config", "user.name", "Operator")
	archGit(t, root, "config", "user.email", "op@example.com")
	archGit(t, root, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(root, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	archGit(t, root, "add", "stray.txt")

	second, err := archiveRun(rec, transcript, []byte("o"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two runs share a directory")
	}
	rel, _ := filepath.Rel(root, second)
	for _, f := range strings.Split(archGit(t, root, "show", "--name-only", "--pretty=format:", "HEAD"), "\n") {
		if !strings.HasPrefix(f, filepath.ToSlash(rel)+"/") {
			t.Errorf("the commit holds %q outside %s", f, rel)
		}
	}
	if got := archGit(t, root, "log", "-1", "--format=%an|%ae"); got != "Operator|op@example.com" {
		t.Fatalf("identity %q", got)
	}
	if st := archGit(t, root, "status", "--porcelain"); st != "A  stray.txt" {
		t.Fatalf("status %q", st)
	}
	if n := archGit(t, root, "rev-list", "--count", "HEAD"); n != "2" {
		t.Fatalf("commits %s", n)
	}
}

func TestArchiveRun_ATranscriptThatCannotBeReadIsAnError(t *testing.T) {
	archiveRunWorld(t)
	rec := runRecord{Fixture: "f", Model: "m", StartedAt: time.Now()}
	if _, err := archiveRun(rec, filepath.Join(t.TempDir(), "gone.jsonl"), nil, nil, nil); err == nil || !strings.Contains(err.Error(), "archive transcript") {
		t.Fatalf("err = %v", err)
	}
}

// Committing a directory that is already committed is not an error (a retried archive).
func TestArchiveRun_CommittingTheSameRunTwiceIsNotAnError(t *testing.T) {
	root, transcript := archiveRunWorld(t)
	rec := runRecord{Fixture: "f", Model: "m", Passed: true, StartedAt: time.Now()}
	dir, err := archiveRun(rec, transcript, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitRun(root, dir, rec); err != nil {
		t.Fatalf("second commit of an unchanged run: %v", err)
	}
	if n := archGit(t, root, "rev-list", "--count", "HEAD"); n != "1" {
		t.Fatalf("commits %s", n)
	}
}

func readTestFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func keys(m map[string]string) []string {
	var k []string
	for n := range m {
		k = append(k, n)
	}
	sort.Strings(k)
	return k
}
