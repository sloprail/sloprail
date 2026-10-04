package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T057_07: the temp dir is found from the transcript, which names it, even where the operator's
// environment says nothing about Claude Code's temp root (no CLAUDE_CODE_TMPDIR, and the default
// roots hold nothing of this session).
func TestT057_07_TheTempDirIsFoundFromTheTranscript(t *testing.T) {
	w := newWorld(t)
	const sess = "s-057-07"
	w.session(t, sess, background(sess))
	mustWrite(t, filepath.Join(w.tempDir(t, sess), "scratchpad", "plan.md"), "named by the transcript\n")

	r := w.e.CLIDirectEnv(w.proj, w.e.SessionEnv(""), "sr-eval", "archive", "--session", sess, "--into", w.into)
	if r.Code != 0 {
		t.Fatalf("archive: exit %d:\n%s", r.Code, r.Output)
	}
	dir := strings.TrimSpace(r.Output)
	if got := readFile(t, filepath.Join(dir, sess, "tmp", "scratchpad", "plan.md")); got != "named by the transcript\n" {
		t.Fatalf("scratchpad: %q", got)
	}
	m := readManifest(t, dir)
	if how := m.Sources.Scratchpad[sess]; !strings.Contains(how, "named in the transcript") {
		t.Fatalf("the temp dir was not found through the transcript: %q", how)
	}
	for _, s := range m.Skipped {
		// the session has no sub-agents or tool results, so its session dir is rightly absent
		if s.Item != "session dir (subagents, tool-results)" {
			t.Errorf("skipped: %+v", s)
		}
	}
}

// T057_08: what a session left behind may be gone — its temp dir, a repository it tracked — and
// that is recorded in the manifest, never a failure: the rest of the archive is whole.
func TestT057_08_WhatIsGoneIsReportedNotFatal(t *testing.T) {
	w := newWorld(t)
	const sess = "s-057-08"
	other := w.e.Project()
	w.e.GitInit(other)
	w.e.FileGuard(other, "docs", judgeRule, map[string]string{"rubric.md.j2": rubric})
	w.e.CommitAll(other, "the rule")
	// no background task: the session has no temp dir at all
	w.session(t, sess, harness.Bash("other-"+sess, fmt.Sprintf(
		"mkdir -p %[1]s/docs && echo second > %[1]s/docs/b.md && git -C %[1]s add -A && git -C %[1]s commit -q -m b", other)))
	if err := os.RemoveAll(other); err != nil {
		t.Fatal(err)
	}

	dir := w.archive(t, "--session", sess)

	if !exists(filepath.Join(dir, sess, "transcript.jsonl")) || len(stateFiles(t, dir, sess)) != 1 {
		t.Fatalf("the session is not whole")
	}
	m := readManifest(t, dir)
	if len(m.Checks) != 1 || m.Checks[0].Repo == other {
		t.Fatalf("want the project's checks alone, got %+v", m.Checks)
	}
	var tmpGone, repoGone bool
	for _, s := range m.Skipped {
		tmpGone = tmpGone || (s.Session == sess && s.Item == "scratchpad and tasks")
		repoGone = repoGone || (strings.HasPrefix(s.Item, "checks of ") && strings.HasSuffix(s.Item, filepath.Base(other)) && strings.Contains(s.Reason, "gone"))
	}
	if !tmpGone || !repoGone {
		t.Fatalf("want the missing temp dir and the gone repository reported, got %+v", m.Skipped)
	}
	if exists(filepath.Join(dir, sess, "tmp")) {
		t.Fatalf("a temp dir was invented")
	}
}

// T057_09: a session whose transcript is a continuation (a fork, a resume into a new file) keeps its
// state store under the conversation's origin, not under the transcript's own id; the archive
// finds it there.
func TestT057_09_AContinuationsStateIsKeyedByItsOrigin(t *testing.T) {
	w := newWorld(t)
	const old, cont = "s-057-09-old", "s-057-09-new"
	w.session(t, old)
	w.e.Fork(w.proj, old, cont)

	dir := w.archive(t, "--session", cont)

	files := stateFiles(t, dir, cont)
	if len(files) != 1 {
		t.Fatalf("want the origin's state.db archived for the continuation, got %v", files)
	}
	origin := w.e.OriginRecord(w.proj, old)
	for rel := range files {
		if !strings.HasPrefix(rel, origin+string(filepath.Separator)) {
			t.Fatalf("state under %q, want the origin %q", rel, origin)
		}
	}
}

// T057_10: with no --into, the archive goes under $SLOPRAIL_SESSION_ARCHIVE_DIR, else under the
// XDG data dir (pinned inside the test, so the operator's real one is never touched).
func TestT057_10_TheDefaultArchiveLocation(t *testing.T) {
	w := newWorld(t)
	const sess = "s-057-10"
	w.session(t, sess)

	xdg := t.TempDir()
	envDir := filepath.Join(t.TempDir(), "by-env")
	env := append(w.archiveEnv(), "XDG_DATA_HOME="+xdg, "SLOPRAIL_SESSION_ARCHIVE_DIR="+envDir)
	r := w.e.CLIDirectEnv(w.proj, env, "sr-eval", "archive", "--session", sess)
	if r.Code != 0 || !strings.HasPrefix(strings.TrimSpace(r.Output), envDir) {
		t.Fatalf("env override: exit %d:\n%s", r.Code, r.Output)
	}
	if n := git(t, envDir, "rev-list", "--count", "HEAD"); n != "1" {
		t.Fatalf("commits: %s", n)
	}

	r = w.e.CLIDirectEnv(w.proj, append(w.archiveEnv(), "XDG_DATA_HOME="+xdg, "SLOPRAIL_SESSION_ARCHIVE_DIR="), "sr-eval", "archive", "--session", sess)
	want := filepath.Join(xdg, "sloprail", "session-archives")
	if r.Code != 0 || !strings.HasPrefix(strings.TrimSpace(r.Output), want) {
		t.Fatalf("XDG default: exit %d, want under %s:\n%s", r.Code, want, r.Output)
	}
}

// T057_11: a session that left no state store (a transcript and nothing else) is archived with
// the absence recorded against it, and the manifest names the tools that made the archive.
func TestT057_11_ASessionWithoutAStateStoreAndTheToolVersions(t *testing.T) {
	w := newWorld(t)
	const sess = "s-057-12"
	w.session(t, "s-057-12-other")
	mustWrite(t, w.e.TranscriptPath(w.proj, sess),
		fmt.Sprintf(`{"type":"user","uuid":"u-%s","parentUuid":null,"cwd":%q,"message":{"role":"user","content":"hi"}}`+"\n", sess, w.proj))

	dir := w.archive(t, "--session", sess)

	if !exists(filepath.Join(dir, sess, "transcript.jsonl")) {
		t.Fatalf("the transcript is not archived")
	}
	if len(stateFiles(t, dir, sess)) != 0 {
		t.Fatalf("a state store was invented: %v", stateFiles(t, dir, sess))
	}
	m := readManifest(t, dir)
	var absent bool
	for _, s := range m.Skipped {
		absent = absent || (s.Session == sess && s.Item == "sloprail state store")
	}
	if !absent {
		t.Fatalf("the missing state store is not reported: %+v", m.Skipped)
	}
	// the project's own repository is archived whatever the session tracked
	if len(m.Checks) != 1 || !strings.Contains(readFile(t, filepath.Join(dir, m.Checks[0].File)), failText) {
		t.Fatalf("want the project's stored verdicts, got %+v", m.Checks)
	}
	for _, tool := range []string{"sr-eval", "sr-session", "sr-checks", "git"} {
		if v := m.Tools[tool]; v == "" || v == "unavailable" {
			t.Errorf("tool_versions[%s] = %q", tool, v)
		}
	}
}
