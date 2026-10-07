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
// roots hold nothing of this session). A harness with no temp dir has nothing to find, and the
// archive neither invents one nor reports one missing.
func TestT057_07_TheTempDirIsFoundFromTheTranscript(t *testing.T) {
	w := newWorld(t)
	const sess = "s-057-07"
	w.session(t, sess, background(t, sess))
	sid := w.id(sess)
	if keepsTempDir(t) {
		mustWrite(t, filepath.Join(w.tempDir(t, sess), "scratchpad", "plan.md"), "named by the transcript\n")
	}

	r := w.e.CLIDirectEnv(w.proj, w.e.SessionEnv(""), "sr-eval", "archive", "--session", sid, "--into", w.into)
	if r.Code != 0 {
		t.Fatalf("archive: exit %d:\n%s", r.Code, r.Output)
	}
	dir := strings.TrimSpace(r.Output)
	m := readManifest(t, dir)
	if keepsTempDir(t) {
		if got := readFile(t, filepath.Join(dir, sid, "tmp", "scratchpad", "plan.md")); got != "named by the transcript\n" {
			t.Fatalf("scratchpad: %q", got)
		}
		if how := m.Sources.Companions[sid]["scratchpad and tasks"]; !strings.Contains(how, "named in the transcript") {
			t.Fatalf("the temp dir was not found through the transcript: %q", how)
		}
	} else if exists(filepath.Join(dir, sid, "tmp")) {
		t.Fatalf("a temp dir was invented for a harness that keeps none")
	}
	for _, s := range m.Skipped {
		// the session has no tool results, so its session dir is rightly absent
		if s.Item != "session dir (tool-results)" {
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
	sid := w.id(sess)

	dir := w.archive(t, "--session", sess)

	if !exists(filepath.Join(dir, sid, "transcript.jsonl")) || len(stateFiles(t, dir, sid)) != 1 {
		t.Fatalf("the session is not whole")
	}
	m := readManifest(t, dir)
	if len(m.Checks) != 1 || m.Checks[0].Repo == other {
		t.Fatalf("want the project's checks alone, got %+v", m.Checks)
	}
	var tmpGone, repoGone bool
	for _, s := range m.Skipped {
		tmpGone = tmpGone || (s.Session == sid && s.Item == "scratchpad and tasks")
		repoGone = repoGone || (strings.HasPrefix(s.Item, "checks of ") && strings.HasSuffix(s.Item, filepath.Base(other)) && strings.Contains(s.Reason, "gone"))
	}
	// a harness with a temp dir reports its absence; one with none has nothing to report
	if tmpGone != keepsTempDir(t) || !repoGone {
		t.Fatalf("want the gone repository reported, and the missing temp dir exactly where the harness keeps one (%v), got %+v", keepsTempDir(t), m.Skipped)
	}
	if exists(filepath.Join(dir, sid, "tmp")) {
		t.Fatalf("a temp dir was invented")
	}
}

// T057_09: a session whose transcript is a continuation (a fork, a resume into a new file) keeps its
// state store under the conversation's origin, not under the transcript's own id; the archive
// finds it there. A harness whose sessions cannot be forked (no CapForkSessions) continues one by
// resuming it: the conversation is the same session, and its state is archived under it.
func TestT057_09_AContinuationsStateIsKeyedByItsOrigin(t *testing.T) {
	w := newWorld(t)
	const old, cont = "s-057-09-old", "s-057-09-new"
	w.session(t, old)

	if !harness.HasCap(t, harness.CapForkSessions) {
		w.e.Run(w.proj, old, "carry on", harness.Turns("done"))
		dir := w.archive(t, "--session", old)
		if files := stateFiles(t, dir, w.id(old)); len(files) != 1 {
			t.Fatalf("want the resumed session's state.db archived, got %v", files)
		}
		return
	}
	w.e.RunForked(w.proj, old, cont, "carry on", harness.Turns("done"))

	dir := w.archive(t, "--session", cont)

	files := stateFiles(t, dir, w.id(cont))
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
	sid := w.id(sess)

	xdg := t.TempDir()
	envDir := filepath.Join(t.TempDir(), "by-env")
	env := append(w.archiveEnv(), "XDG_DATA_HOME="+xdg, "SLOPRAIL_SESSION_ARCHIVE_DIR="+envDir)
	r := w.e.CLIDirectEnv(w.proj, env, "sr-eval", "archive", "--session", sid)
	if r.Code != 0 || !strings.HasPrefix(strings.TrimSpace(r.Output), envDir) {
		t.Fatalf("env override: exit %d:\n%s", r.Code, r.Output)
	}
	if n := git(t, envDir, "rev-list", "--count", "HEAD"); n != "1" {
		t.Fatalf("commits: %s", n)
	}

	r = w.e.CLIDirectEnv(w.proj, append(w.archiveEnv(), "XDG_DATA_HOME="+xdg, "SLOPRAIL_SESSION_ARCHIVE_DIR="), "sr-eval", "archive", "--session", sid)
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
	w.e.ForgeBareTranscript(w.proj, sess)
	sid := w.id(sess)

	dir := w.archive(t, "--session", sess)

	if !exists(filepath.Join(dir, sid, "transcript.jsonl")) {
		t.Fatalf("the transcript is not archived")
	}
	if len(stateFiles(t, dir, sid)) != 0 {
		t.Fatalf("a state store was invented: %v", stateFiles(t, dir, sid))
	}
	m := readManifest(t, dir)
	var absent bool
	for _, s := range m.Skipped {
		absent = absent || (s.Session == sid && s.Item == "sloprail state store")
	}
	if !absent {
		t.Fatalf("the missing state store is not reported: %+v", m.Skipped)
	}
	// the project's own repository has its file whatever the session tracked, but this session
	// tracked no range, so it relied on none of the verdicts the other session left there
	if whole := w.e.CLIDirect(w.proj, "sr-checks", "log", "--json"); !strings.Contains(whole.Output, failText) {
		t.Fatalf("premise: the repository holds the other session's verdict: %s", whole.Output)
	}
	if len(m.Checks) != 1 || readFile(t, filepath.Join(dir, m.Checks[0].File)) != "" {
		t.Fatalf("want the project's checks file, empty: %+v", m.Checks)
	}
	for _, tool := range []string{"sr-eval", "sr-session", "sr-checks", "git"} {
		if v := m.Tools[tool]; v == "" || v == "unavailable" {
			t.Errorf("tool_versions[%s] = %q", tool, v)
		}
	}
}
