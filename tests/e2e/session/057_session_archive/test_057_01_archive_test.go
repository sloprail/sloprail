package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T057_01: one session's archive holds everything it left behind — the transcript, the session
// directory, the temp dir (scratchpad and tasks), the state store, the tracked ranges and the
// stored check verdicts of every repository it tracked — and is committed alone.
func TestT057_01_ASessionIsArchivedWithEverythingItLeftBehind(t *testing.T) {
	w := newWorld(t)
	e := w.e
	const sess = "s-057-01"

	// A second tracked repository, outside the project, with its own file-guard.
	other := e.Project()
	e.GitInit(other)
	e.FileGuard(other, "docs", judgeRule, map[string]string{"rubric.md.j2": rubric})
	e.CommitAll(other, "the rule")
	w.session(t, sess, background(sess), harness.Bash("other-"+sess, fmt.Sprintf(
		"mkdir -p %[1]s/docs && echo second > %[1]s/docs/b.md && git -C %[1]s add -A && git -C %[1]s commit -q -m b", other)))

	// What the harness leaves around a real session is a transcript and a state store; the
	// session directory and the scratchpad are Claude Code's own, so put what it would there.
	transcript := e.TranscriptPath(w.proj, sess)
	sessDir := strings.TrimSuffix(transcript, ".jsonl")
	mustWrite(t, filepath.Join(sessDir, "subagents", "agent-a.jsonl"), "subagent record\n")
	mustWrite(t, filepath.Join(sessDir, "tool-results", "r.txt"), "a big tool result\n")
	temp := w.tempDir(t, sess)
	mustWrite(t, filepath.Join(temp, "scratchpad", "notes", "plan.md"), "the plan\n")

	// A stray file staged in the archive repository by hand must not be swept into the commit.
	git(t, w.into, "init", "-q")
	mustWrite(t, filepath.Join(w.into, "stray.txt"), "not an entry\n")
	git(t, w.into, "add", "stray.txt")

	dir := w.archive(t, "--session", sess)

	// the transcript, byte for byte
	if got, want := readFile(t, filepath.Join(dir, sess, "transcript.jsonl")), readFile(t, transcript); got != want {
		t.Fatalf("the archived transcript differs from the original")
	}
	// the session directory
	if got := readFile(t, filepath.Join(dir, sess, "session-dir", "subagents", "agent-a.jsonl")); got != "subagent record\n" {
		t.Fatalf("subagent record: %q", got)
	}
	if got := readFile(t, filepath.Join(dir, sess, "session-dir", "tool-results", "r.txt")); got != "a big tool result\n" {
		t.Fatalf("tool result: %q", got)
	}
	// the temp dir: scratchpad and the task output the mock wrote
	if got := readFile(t, filepath.Join(dir, sess, "tmp", "scratchpad", "notes", "plan.md")); got != "the plan\n" {
		t.Fatalf("scratchpad: %q", got)
	}
	tasks, _ := filepath.Glob(filepath.Join(dir, sess, "tmp", "tasks", "*.output"))
	if len(tasks) != 1 {
		t.Fatalf("the task output is not archived: %v", tasks)
	}
	// the state store: every file of the real one, byte for byte
	real, _ := filepath.Glob(filepath.Join(e.HomeDir(), "*", "*", "sloprail", "sessions", "*", "*", "state.db"))
	if len(real) != 1 {
		t.Fatalf("want one real state.db under %s, got %v", e.HomeDir(), real)
	}
	copied := stateFiles(t, dir, sess)
	if len(copied) != 1 {
		t.Fatalf("want the state.db copied, got %v", copied)
	}
	for _, p := range copied {
		if !bytes.Equal([]byte(readFile(t, p)), []byte(readFile(t, real[0]))) {
			t.Fatalf("the archived state.db differs from the real one")
		}
	}
	// the tracked ranges, as sr-session lists them right now
	refs := e.CLIDirectEnv(w.proj, e.SessionEnv(sess), "sr-session", "refs", "list", "--json")
	if refs.Code != 0 {
		t.Fatalf("refs list: %s", refs.Output)
	}
	if got := readFile(t, filepath.Join(dir, sess, "refs.json")); got != refs.Output {
		t.Fatalf("refs.json differs from `sr-session refs list --json`:\n%s\nvs\n%s", got, refs.Output)
	}
	var ranges []struct{ Folder, Head string }
	if err := json.Unmarshal([]byte(refs.Output), &ranges); err != nil || len(ranges) != 2 {
		t.Fatalf("want the project and the other repository tracked, got %s (%v)", refs.Output, err)
	}
	// the stored verdicts of each tracked repository
	m := readManifest(t, dir)
	if len(m.Checks) != 2 {
		t.Fatalf("want checks for both repositories, got %+v", m.Checks)
	}
	for _, c := range m.Checks {
		want := e.CLIDirect(c.Repo, "sr-checks", "log", "--json")
		if want.Code != 0 || !strings.Contains(want.Output, failText) {
			t.Fatalf("premise: %s has no stored FAIL: %s", c.Repo, want.Output)
		}
		if got := readFile(t, filepath.Join(dir, c.File)); got != want.Output {
			t.Fatalf("%s: archived checks differ from `sr-checks log --json`:\n%s\nvs\n%s", c.Repo, got, want.Output)
		}
		if len(c.Sessions) != 1 || c.Sessions[0] != sess {
			t.Fatalf("%s tracked by %v", c.Repo, c.Sessions)
		}
	}
	for _, s := range m.Skipped {
		t.Errorf("skipped %+v", s)
	}

	// committed: one new commit holding exactly this entry's files; the stray stays staged
	rel, _ := filepath.Rel(w.into, dir)
	if n := git(t, w.into, "rev-list", "--count", "HEAD"); n != "1" {
		t.Fatalf("want one commit, got %s", n)
	}
	for _, f := range strings.Fields(git(t, w.into, "show", "--name-only", "--pretty=format:", "HEAD")) {
		if !strings.HasPrefix(f, filepath.ToSlash(rel)+"/") {
			t.Fatalf("the commit holds %q, outside its own directory %s", f, rel)
		}
	}
	if st := git(t, w.into, "status", "--porcelain"); st != "A  stray.txt" {
		t.Fatalf("the entry was not committed whole, or the stray file was: %q", st)
	}
	if subj := git(t, w.into, "log", "-1", "--format=%s"); !strings.Contains(subj, "session-archive") {
		t.Fatalf("commit message %q", subj)
	}
}

// T057_02: --all-sessions takes every session of the project into one entry; --label names its
// directory (by default the Claude Code project directory's name); --into picks the repository.
func TestT057_02_AllSessionsLabelAndInto(t *testing.T) {
	w := newWorld(t)
	w.session(t, "s-057-02a")
	w.session(t, "s-057-02b")

	dir := w.archive(t, "--all-sessions")
	m := readManifest(t, dir)
	if strings.Join(m.Sessions, ",") != "s-057-02a,s-057-02b" {
		t.Fatalf("sessions: %v", m.Sessions)
	}
	for _, id := range m.Sessions {
		if !exists(filepath.Join(dir, id, "transcript.jsonl")) || len(stateFiles(t, dir, id)) != 1 {
			t.Fatalf("session %s is not whole in the archive", id)
		}
	}
	// the default label is the project directory's name in Claude Code's config dir
	projName := filepath.Base(filepath.Dir(w.e.TranscriptPath(w.proj, "s-057-02a")))
	if filepath.Dir(dir) != filepath.Join(w.into, projName) || m.Label != projName {
		t.Fatalf("default label: entry %s, label %q, want %q", dir, m.Label, projName)
	}

	// an explicit label, in the same repository: a second commit, its own directory only
	dir2 := w.archive(t, "--session", "s-057-02b", "--label", "my-label")
	if filepath.Dir(dir2) != filepath.Join(w.into, "my-label") {
		t.Fatalf("label: %s", dir2)
	}
	if n := git(t, w.into, "rev-list", "--count", "HEAD"); n != "2" {
		t.Fatalf("want two commits, got %s", n)
	}
	rel, _ := filepath.Rel(w.into, dir2)
	for _, f := range strings.Fields(git(t, w.into, "show", "--name-only", "--pretty=format:", "HEAD")) {
		if !strings.HasPrefix(f, filepath.ToSlash(rel)+"/") {
			t.Fatalf("the second commit holds %q outside %s", f, rel)
		}
	}
	// a label is a plain name
	r := w.archiveIn(t, w.proj, "--session", "s-057-02a", "--into", w.into, "--label", "../escape")
	if r.Code == 0 || !strings.Contains(r.Output, "plain name") {
		t.Fatalf("a path as a label was accepted: %d %s", r.Code, r.Output)
	}
	// --session and --all-sessions exclude each other; one of them is required
	if r := w.archiveIn(t, w.proj, "--session", "s-057-02a", "--all-sessions", "--into", w.into); r.Code == 0 || !strings.Contains(r.Output, "exclusive") {
		t.Fatalf("both flags accepted: %s", r.Output)
	}
	if r := w.archiveIn(t, w.proj, "--into", w.into); r.Code == 0 {
		t.Fatalf("no flag accepted: %s", r.Output)
	}
}

// T057_03: a git repository inside the scratchpad (a clone the agent made) is archived without
// failing the commit — a copied .git would stage as a gitlink "with no commit checked out" —
// and the repository's working files are kept while its .git is reported as not copied.
func TestT057_03_ANestedRepositoryInTheScratchpad(t *testing.T) {
	w := newWorld(t)
	const sess = "s-057-03"
	w.session(t, sess, background(sess))
	scratch := filepath.Join(w.tempDir(t, sess), "scratchpad", "clone")
	mustWrite(t, filepath.Join(scratch, "file.txt"), "tracked in the clone\n")
	git(t, scratch, "init", "-q")
	git(t, scratch, "add", "-A")
	git(t, scratch, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "in the clone")
	// and an empty one, which has no commit at all
	empty := filepath.Join(w.tempDir(t, sess), "scratchpad", "empty-clone")
	mustWrite(t, filepath.Join(empty, "wip.txt"), "no commit yet\n")
	git(t, empty, "init", "-q")
	// and a linked worktree / submodule checkout, whose .git is a file
	mustWrite(t, filepath.Join(w.tempDir(t, sess), "scratchpad", "linked", "x.txt"), "in a linked worktree\n")
	mustWrite(t, filepath.Join(w.tempDir(t, sess), "scratchpad", "linked", ".git"), "gitdir: /nonexistent/.git/worktrees/linked\n")

	// and what a copy cannot hold: a named pipe, a symlink to a directory
	pad := filepath.Join(w.tempDir(t, sess), "scratchpad")
	if err := syscall.Mkfifo(filepath.Join(pad, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(pad, filepath.Join(pad, "loop")); err != nil {
		t.Fatal(err)
	}

	dir := w.archive(t, "--session", sess)

	for _, odd := range []string{"scratchpad/pipe", "scratchpad/loop"} {
		var found bool
		for _, s := range readManifest(t, dir).Skipped {
			found = found || (strings.Contains(s.Item, odd+": not a regular file") && s.Reason == "not copied")
		}
		if !found || exists(filepath.Join(dir, sess, "tmp", odd)) {
			t.Errorf("%s: want it reported as skipped and not copied (found %v)", odd, found)
		}
	}

	for _, c := range []string{"clone/file.txt", "empty-clone/wip.txt"} {
		if !exists(filepath.Join(dir, sess, "tmp", "scratchpad", c)) {
			t.Errorf("%s is not archived", c)
		}
	}
	if exists(filepath.Join(dir, sess, "tmp", "scratchpad", "clone", ".git")) {
		t.Errorf("a nested .git was copied")
	}
	var nested int
	for _, s := range readManifest(t, dir).Skipped {
		if strings.Contains(s.Reason, "not copied") && strings.Contains(s.Item, ".git") {
			nested++
		}
	}
	if nested != 2 {
		t.Errorf("want both nested .git directories reported as skipped, got %d", nested)
	}
	if st := git(t, w.into, "status", "--porcelain"); st != "" {
		t.Fatalf("the archive repository is not clean after the commit: %q", st)
	}
	staged := git(t, w.into, "ls-files", "--stage")
	if !strings.Contains(staged, "scratchpad/clone/file.txt") || !strings.Contains(staged, "scratchpad/empty-clone/wip.txt") || !strings.Contains(staged, "scratchpad/linked/x.txt") {
		t.Fatalf("the nested repositories' files are not committed:\n%s", staged)
	}
	for _, l := range strings.Split(staged, "\n") {
		if strings.HasPrefix(l, "160000") {
			t.Fatalf("a gitlink was committed: %s", l)
		}
	}
}

// T057_04: symlinked roots — a temp root reached through a symlink (macOS /tmp -> /private/tmp)
// and a scratchpad that is itself a symlink — are followed; and the project reached through a
// symlink is the same project.
func TestT057_04_SymlinkedRoots(t *testing.T) {
	w := newWorld(t)
	const sess = "s-057-04"
	w.session(t, sess) // no background task: the transcript does not name the temp dir
	// the session directory (sub-agent records) is itself a symlink to where Claude Code kept it
	realSessDir := filepath.Join(filepath.Dir(w.e.TmpDir()), "real-session-dir")
	mustWrite(t, filepath.Join(realSessDir, "subagents", "agent-a.jsonl"), "behind a symlink too\n")
	if err := os.Symlink(realSessDir, strings.TrimSuffix(w.e.TranscriptPath(w.proj, sess), ".jsonl")); err != nil {
		t.Fatal(err)
	}

	// the temp dir exists only under the env's temp root, which is reached through a symlink
	projName := filepath.Base(filepath.Dir(w.e.TranscriptPath(w.proj, sess)))
	realTmp := filepath.Join(w.e.TmpDir(), "elsewhere")
	mustWrite(t, filepath.Join(realTmp, "scratchpad", "notes.txt"), "behind a symlink\n")
	linkedTmp := filepath.Join(filepath.Dir(w.e.TmpDir()), "tmp-link")
	if err := os.Symlink(w.e.TmpDir(), linkedTmp); err != nil {
		t.Fatal(err)
	}
	uidDir := fmt.Sprintf("claude-%d", os.Getuid())
	if err := os.MkdirAll(filepath.Join(w.e.TmpDir(), uidDir, projName), 0o755); err != nil {
		t.Fatal(err)
	}
	// <tmp>/claude-<uid>/<project>/<session> is itself a symlink to the real directory
	if err := os.Symlink(realTmp, filepath.Join(w.e.TmpDir(), uidDir, projName, sess)); err != nil {
		t.Fatal(err)
	}
	// the project, through a symlink
	linkedProj := filepath.Join(filepath.Dir(w.e.TmpDir()), "proj-link")
	if err := os.Symlink(w.proj, linkedProj); err != nil {
		t.Fatal(err)
	}

	env := append(w.e.SessionEnv(""), "CLAUDE_CODE_TMPDIR="+linkedTmp)
	r := w.e.CLIDirectEnv(linkedProj, env, "sr-eval", "archive", "--session", sess, "--into", w.into)
	if r.Code != 0 {
		t.Fatalf("archive through symlinks: exit %d:\n%s", r.Code, r.Output)
	}
	dir := strings.TrimSpace(r.Output)
	if got := readFile(t, filepath.Join(dir, sess, "tmp", "scratchpad", "notes.txt")); got != "behind a symlink\n" {
		t.Fatalf("the scratchpad behind symlinks: %q", got)
	}
	if got := readFile(t, filepath.Join(dir, sess, "session-dir", "subagents", "agent-a.jsonl")); got != "behind a symlink too\n" {
		t.Fatalf("the session directory behind a symlink: %q", got)
	}
	if len(stateFiles(t, dir, sess)) != 1 || !exists(filepath.Join(dir, sess, "transcript.jsonl")) {
		t.Fatalf("the session is not whole when the project is reached through a symlink")
	}
	if len(readManifest(t, dir).Checks) == 0 {
		t.Fatalf("no checks archived for the project reached through a symlink")
	}
}

// T057_05: a session id that does not exist is a clean error: named, exit non-zero, and nothing
// is created or committed. Neither is a half-archive made when only one of two ids is missing.
func TestT057_05_AnUnknownSessionIsACleanError(t *testing.T) {
	w := newWorld(t)
	w.session(t, "s-057-05")

	r := w.archiveIn(t, w.proj, "--session", "no-such-session", "--into", w.into)
	if r.Code == 0 || !strings.Contains(r.Output, "no-such-session") || !strings.Contains(r.Output, "no transcript") {
		t.Fatalf("unknown session: exit %d:\n%s", r.Code, r.Output)
	}
	if strings.Contains(r.Output, "panic") || strings.Contains(r.Output, "goroutine") {
		t.Fatalf("the error is a crash:\n%s", r.Output)
	}
	r = w.archiveIn(t, w.proj, "--session", "s-057-05", "--session", "no-such-session", "--into", w.into)
	if r.Code == 0 {
		t.Fatalf("one of two sessions missing was accepted:\n%s", r.Output)
	}
	if exists(w.into) && len(readDir(t, w.into)) > 0 {
		t.Fatalf("a failed archive left something in %s: %v", w.into, readDir(t, w.into))
	}
	// an id that is a path is refused, not followed
	r = w.archiveIn(t, w.proj, "--session", "../s-057-05", "--into", w.into)
	if r.Code == 0 || !strings.Contains(r.Output, "not a session id") {
		t.Fatalf("a path as session id: exit %d:\n%s", r.Code, r.Output)
	}
}

// T057_06: the working directory defines the project: another project's sessions are not
// reachable from here, --all-sessions takes only this project's, and a directory Claude Code never
// ran in has no project.
func TestT057_06_TheWorkingDirectoryDefinesTheProject(t *testing.T) {
	w := newWorld(t)
	w.session(t, "s-057-06-a")
	second := w.e.Project()
	w.e.GitInit(second)
	w.e.FileGuard(second, "docs", judgeRule, map[string]string{"rubric.md.j2": rubric})
	w.e.CommitAll(second, "the rule")
	w.sessionIn(t, second, "s-057-06-b")

	r := w.archiveIn(t, second, "--session", "s-057-06-a", "--into", w.into)
	if r.Code == 0 || !strings.Contains(r.Output, "s-057-06-a") {
		t.Fatalf("another project's session was archived from here: exit %d:\n%s", r.Code, r.Output)
	}
	r = w.archiveIn(t, second, "--all-sessions", "--into", w.into)
	if r.Code != 0 {
		t.Fatalf("all-sessions in the second project: %s", r.Output)
	}
	dir := strings.TrimSpace(r.Output)
	if m := readManifest(t, dir); strings.Join(m.Sessions, ",") != "s-057-06-b" || m.Cwd == w.proj {
		t.Fatalf("the second project's archive holds %v (cwd %s)", m.Sessions, m.Cwd)
	}
	if !exists(filepath.Join(dir, "s-057-06-b", "transcript.jsonl")) || exists(filepath.Join(dir, "s-057-06-a")) {
		t.Fatalf("the archive is not the second project's alone")
	}
	// the checks are those of the second project's repository, not the first's
	var repos []string
	for _, c := range readManifest(t, dir).Checks {
		repos = append(repos, c.Repo)
	}
	if len(repos) != 1 || !strings.HasSuffix(repos[0], filepath.Base(second)) {
		t.Fatalf("checks of %v, want only %s", repos, second)
	}

	nowhere := t.TempDir()
	r = w.archiveIn(t, nowhere, "--all-sessions", "--into", w.into)
	if r.Code == 0 || !strings.Contains(r.Output, "no Claude Code project directory") {
		t.Fatalf("a directory with no project: exit %d:\n%s", r.Code, r.Output)
	}
}

// T057_07: the stored verdicts live in the repository's own ref, shared by all its worktrees, so a
// session that tracked several worktrees of one repository gets ONE checks file for it (not a copy
// per worktree), beside one for a second repository; archive.json maps every tracked folder to it.
func TestT057_07_WorktreesOfOneRepositoryShareOneChecksFile(t *testing.T) {
	w := newWorld(t)
	e := w.e
	const sess = "s-057-07"

	root, err := os.MkdirTemp("", "slop-wt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	if root, err = filepath.EvalSymlinks(root); err != nil {
		t.Fatal(err)
	}
	wt1, wt2 := filepath.Join(root, "wt1"), filepath.Join(root, "wt2")
	git(t, w.proj, "worktree", "add", "-q", "-b", "wt-one", wt1)
	git(t, w.proj, "worktree", "add", "-q", "-b", "wt-two", wt2)

	other := e.Project()
	e.GitInit(other)
	e.FileGuard(other, "docs", judgeRule, map[string]string{"rubric.md.j2": rubric})
	e.CommitAll(other, "the rule")

	commitIn := func(dir, name string) harness.Turn {
		return harness.Bash("c-"+name, fmt.Sprintf(
			"mkdir -p %[1]s/docs && echo %[2]s > %[1]s/docs/%[2]s.md && git -C %[1]s add -A && git -C %[1]s commit -q -m %[2]s", dir, name))
	}
	w.session(t, sess, background(sess), commitIn(wt1, "one"), commitIn(wt2, "two"), commitIn(other, "three"))

	dir := w.archive(t, "--session", sess)
	m := readManifest(t, dir)
	for _, s := range m.Skipped {
		if !strings.HasPrefix(s.Item, "session dir") { // the mock leaves no session directory
			t.Errorf("skipped %+v", s)
		}
	}
	if len(m.Checks) != 2 {
		t.Fatalf("want one checks entry per repository (2), got %+v", m.Checks)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "checks", "*.jsonl"))
	if len(files) != 2 {
		t.Fatalf("want exactly one checks file per repository, got %v", files)
	}

	realProj, _ := filepath.EvalSymlinks(w.proj)
	realOther, _ := filepath.EvalSymlinks(other)
	covered := map[string]bool{}
	for _, c := range m.Checks {
		want := e.CLIDirect(c.Repo, "sr-checks", "log", "--json")
		if want.Code != 0 || !strings.Contains(want.Output, failText) {
			t.Fatalf("premise: %s has no stored FAIL: %s", c.Repo, want.Output)
		}
		if got := readFile(t, filepath.Join(dir, c.File)); got != want.Output {
			t.Fatalf("%s: archived checks differ from `sr-checks log --json`:\n%s\nvs\n%s", c.Repo, got, want.Output)
		}
		for _, f := range c.Folders {
			covered[f] = true
		}
		switch c.Repo {
		case realProj:
			if strings.Join(c.Folders, ",") != strings.Join(sorted(realProj, wt1, wt2), ",") {
				t.Errorf("the project repository's folders: %v", c.Folders)
			}
		case realOther:
			if len(c.Folders) != 1 || c.Folders[0] != realOther {
				t.Errorf("the second repository's folders: %v", c.Folders)
			}
		default:
			t.Errorf("unexpected repository %s", c.Repo)
		}
		if len(c.Sessions) != 1 || c.Sessions[0] != sess {
			t.Errorf("%s tracked by %v", c.Repo, c.Sessions)
		}
	}
	for _, f := range []string{realProj, wt1, wt2, realOther} {
		if !covered[f] {
			t.Errorf("archive.json maps no checks file to the tracked folder %s: %+v", f, m.Checks)
		}
	}
}

func sorted(s ...string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readDir(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		if e.Name() != ".git" {
			names = append(names, e.Name())
		}
	}
	return names
}
