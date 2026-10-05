package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/clidoc"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/transcript"
)

// archiveWorld is a fake machine: a Claude config dir with one project, a temp
// root with a scratchpad, a sloprail data dir with a state store, and stubbed
// sr-session / sr-checks on PATH.
type archiveWorld struct {
	cwd, other, projDir, into, stdinLog string
	cwdSHA, otherSHA                    string // the one commit of each repository
}

// commitEmpty makes a commit in dir and returns its sha. The message names the directory: two root
// commits with the same author, tree, message and second are the same object, which made a "stranger"
// history identical to the base in CI.
func commitEmpty(t *testing.T, dir string) string {
	t.Helper()
	gitOut(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "--quiet", "--allow-empty", "-m", "commit in "+dir+" "+t.Name())
	return gitOut(t, dir, "rev-parse", "HEAD")
}

func gitInit(t *testing.T, dir string) string {
	t.Helper()
	if out, err := exec.Command("git", "-C", dir, "init", "--quiet").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func newArchiveWorld(t *testing.T, sessions ...string) *archiveWorld {
	t.Helper()
	w := &archiveWorld{
		cwd:   gitInit(t, t.TempDir()),
		other: gitInit(t, t.TempDir()),
		into:  filepath.Join(t.TempDir(), "archives"),
	}
	w.cwdSHA, w.otherSHA = commitEmpty(t, w.cwd), commitEmpty(t, w.other)
	cfg := t.TempDir()
	tmp := t.TempDir()
	data := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("CLAUDE_CODE_TMPDIR", tmp)
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Chdir(w.cwd)

	w.projDir = transcript.ProjectDir(cfg, w.cwd)
	proj := filepath.Base(w.projDir)
	for _, id := range sessions {
		mustWriteFile2(t, filepath.Join(w.projDir, id+".jsonl"), `{"type":"user"}`+"\n")
		mustWriteFile2(t, filepath.Join(w.projDir, id, "subagents", "agent-a.jsonl"), "sub\n")
		mustWriteFile2(t, filepath.Join(w.projDir, id, "tool-results", "r.txt"), "result\n")
		root := filepath.Join(tmp, fmt.Sprintf("claude-%d", os.Getuid()), proj, id)
		mustWriteFile2(t, filepath.Join(root, "scratchpad", "notes.txt"), "scratch\n")
		mustWriteFile2(t, filepath.Join(root, "tasks", "t1.output"), "task\n")
		db, err := sessionpath.StateDB(w.cwd, id)
		if err != nil {
			t.Fatal(err)
		}
		mustWriteFile2(t, db, "sqlite-bytes")
	}

	bin := t.TempDir()
	w.stdinLog = filepath.Join(t.TempDir(), "stdin.log")
	mustWriteFile2(t, filepath.Join(bin, "sr-session"), `#!/bin/sh
case "$*" in
  "--version") echo "sr-session version stub-1";;
  "refs list --json") cat >> '`+w.stdinLog+`'; echo >> '`+w.stdinLog+`'
    printf '[{"Folder":"%s","Base":"%s","Head":"main","HeadSHA":"%s"},{"Folder":"%s","Base":"%s","Head":"x","HeadSHA":"%s"},{"Folder":"%s","Base":"%s","Head":"main","HeadSHA":"%s"},{"Folder":"/no/such/folder","Head":"y"}]' \
      '`+w.other+`' '`+gitrepo.EmptyTree+`' '`+w.otherSHA+`' '`+w.other+`' '`+gitrepo.EmptyTree+`' '`+w.otherSHA+`' '`+w.cwd+`' '`+gitrepo.EmptyTree+`' '`+w.cwdSHA+`';;
  *) exit 3;;
esac
`)
	mustWriteFile2(t, filepath.Join(bin, "sr-checks"), `#!/bin/sh
case "$*" in
  "--version") echo "sr-checks version stub-2";;
  "log --json"*) printf '{"rule":"r","repo":"%s","args":"%s"}\n' "$(pwd -P)" "$*";;
  *) exit 3;;
esac
`)
	for _, n := range []string{"sr-session", "sr-checks"} {
		if err := os.Chmod(filepath.Join(bin, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return w
}

func mustWriteFile2(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, path, content)
}

func runArchiveCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"archive"}, args...))
	err := root.Execute()
	return strings.TrimSpace(out.String()), err
}

func TestArchive_CopiesEverythingPerSession(t *testing.T) {
	w := newArchiveWorld(t, "sess-1", "sess-2")
	dir, err := runArchiveCLI(t, "--session", "sess-1", "--session", "sess-2", "--into", w.into, "--label", "lbl")
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if filepath.Dir(dir) != filepath.Join(w.into, "lbl") {
		t.Fatalf("entry %s is not under <into>/<label>", dir)
	}
	for _, id := range []string{"sess-1", "sess-2"} {
		for rel, want := range map[string]string{
			"transcript.jsonl":                    `{"type":"user"}`,
			"session-dir/subagents/agent-a.jsonl": "sub",
			"session-dir/tool-results/r.txt":      "result",
			"tmp/scratchpad/notes.txt":            "scratch",
			"tmp/tasks/t1.output":                 "task",
			"state/" + id + "/state.db":           "sqlite-bytes",
		} {
			got, err := os.ReadFile(filepath.Join(dir, id, rel))
			if err != nil || strings.TrimSpace(string(got)) != want {
				t.Errorf("%s/%s: got %q, %v; want %q", id, rel, got, err, want)
			}
		}
		refs, err := os.ReadFile(filepath.Join(dir, id, "refs.json"))
		if err != nil || !strings.Contains(string(refs), w.other) {
			t.Errorf("%s refs.json: %q, %v", id, refs, err)
		}
	}

	// sr-session ran as the session: its payload named the transcript.
	log, _ := os.ReadFile(w.stdinLog)
	if !strings.Contains(string(log), `"session_id":"sess-2"`) || !strings.Contains(string(log), filepath.Join(w.projDir, "sess-2.jsonl")) {
		t.Errorf("sr-session was not told which session: %s", log)
	}

	var m archiveManifest
	body, err := os.ReadFile(filepath.Join(dir, "archive.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if m.Label != "lbl" || m.Cwd != w.cwd || strings.Join(m.Sessions, ",") != "sess-1,sess-2" {
		t.Errorf("manifest header: %+v", m)
	}
	if m.Tools["sr-session"] != "sr-session version stub-1" || m.Tools["sr-checks"] != "sr-checks version stub-2" || m.Tools["git"] == "" {
		t.Errorf("tool versions: %v", m.Tools)
	}
	if !strings.Contains(m.Sources.Scratchpad["sess-1"], "temp root") {
		t.Errorf("scratchpad source: %v", m.Sources.Scratchpad)
	}

	// Checks: the project repo and the other tracked folder, once each; the
	// vanished folder is a recorded skip, not a failure.
	if len(m.Checks) != 2 {
		t.Fatalf("checks: %+v", m.Checks)
	}
	for _, c := range m.Checks {
		got, err := os.ReadFile(filepath.Join(dir, c.File))
		if err != nil || !strings.Contains(string(got), `"repo":"`+c.Repo+`"`) {
			t.Errorf("%s: %q, %v", c.File, got, err)
		}
		// the log is cut to the session's ranges: one --range per distinct head, none unscoped
		sha := w.otherSHA
		if c.Repo == w.cwd {
			sha = w.cwdSHA
		}
		if want := "log --json --range " + gitrepo.EmptyTree + ".." + sha + `"`; !strings.Contains(string(got), want) {
			t.Errorf("%s: sr-checks was not asked for the session's range %s: %s", c.File, want, got)
		}
	}
	var gone bool
	for _, s := range m.Skipped {
		gone = gone || (strings.Contains(s.Item, "/no/such/folder") && s.Reason == "the folder is gone")
	}
	if !gone {
		t.Errorf("the vanished folder was not recorded as skipped: %+v", m.Skipped)
	}

	// One commit, containing only this entry's directory.
	if n := gitOut(t, w.into, "rev-list", "--count", "HEAD"); n != "1" {
		t.Errorf("commits = %s", n)
	}
	files := gitOut(t, w.into, "show", "--name-only", "--pretty=format:", "HEAD")
	for _, f := range strings.Fields(files) {
		if !strings.HasPrefix(f, "lbl/"+filepath.Base(dir)+"/") {
			t.Errorf("committed %s outside the entry", f)
		}
	}
}

func TestArchive_AllSessionsDefaultsLabelAndSkipsWhatIsAbsent(t *testing.T) {
	w := newArchiveWorld(t, "only")
	// Nothing but the transcript: no session dir, no scratchpad, no state.
	if err := os.RemoveAll(filepath.Join(w.projDir, "only")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CODE_TMPDIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv(sessionArchiveEnv, w.into)
	mustWriteFile2(t, filepath.Join(w.into, "unrelated.txt"), "keep out of the commit")

	dir, err := runArchiveCLI(t, "--all-sessions")
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if want := filepath.Join(w.into, filepath.Base(w.projDir)); filepath.Dir(dir) != want {
		t.Errorf("default label/into: %s, want under %s", dir, want)
	}
	var m archiveManifest
	body, _ := os.ReadFile(filepath.Join(dir, "archive.json"))
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	reasons := map[string]string{}
	for _, s := range m.Skipped {
		reasons[s.Item] = s.Reason
	}
	for _, item := range []string{"session dir (subagents, tool-results)", "scratchpad and tasks", "sloprail state store"} {
		if reasons[item] == "" {
			t.Errorf("%q not recorded as skipped: %v", item, reasons)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "only", "transcript.jsonl")); err != nil {
		t.Errorf("transcript: %v", err)
	}
	if tracked := gitOut(t, w.into, "ls-files"); strings.Contains(tracked, "unrelated.txt") {
		t.Errorf("a file outside the entry was committed: %s", tracked)
	}
}

func TestArchive_StubsMissingFromPathAreSkippedNotFatal(t *testing.T) {
	w := newArchiveWorld(t, "s")
	t.Setenv("PATH", "/usr/bin:/bin")
	dir, err := runArchiveCLI(t, "--session", "s", "--into", w.into)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	var m archiveManifest
	body, _ := os.ReadFile(filepath.Join(dir, "archive.json"))
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	var refs bool
	for _, s := range m.Skipped {
		refs = refs || (s.Item == "refs list --json" && strings.Contains(s.Reason, "sr-session is not on PATH"))
	}
	if !refs {
		t.Errorf("missing tools not recorded: %+v", m.Skipped)
	}
	// without sr-session no range is known, so no verdict is relied on: the repository's file is
	// empty, and sr-checks (not on PATH either) is never asked
	if len(m.Checks) != 1 {
		t.Fatalf("checks: %+v", m.Checks)
	}
	if got, err := os.ReadFile(filepath.Join(dir, m.Checks[0].File)); err != nil || len(got) != 0 {
		t.Errorf("a repository with no tracked range must get an empty file: %q, %v", got, err)
	}
}

func TestArchive_SessionAndAllSessionsAreExactlyOne(t *testing.T) {
	newArchiveWorld(t, "s")
	if _, err := runArchiveCLI(t); err == nil || !strings.Contains(err.Error(), "--session <id>") {
		t.Errorf("neither: %v", err)
	}
	if _, err := runArchiveCLI(t, "--session", "s", "--all-sessions"); err == nil || !strings.Contains(err.Error(), "exclusive") {
		t.Errorf("both: %v", err)
	}
	if _, err := runArchiveCLI(t, "--session", "../x"); err == nil || !strings.Contains(err.Error(), "not a session id") {
		t.Errorf("traversal: %v", err)
	}
	if _, err := runArchiveCLI(t, "--session", "nope"); err == nil || !strings.Contains(err.Error(), "no transcript") {
		t.Errorf("unknown: %v", err)
	}
}

func TestArchive_FindsTempDirNamedInTheTranscript(t *testing.T) {
	w := newArchiveWorld(t, "s")
	elsewhere := filepath.Join(t.TempDir(), "claude-777", "-some-project", "s")
	mustWriteFile2(t, filepath.Join(elsewhere, "scratchpad", "x.txt"), "x")
	mustWriteFile2(t, filepath.Join(w.projDir, "s.jsonl"), `{"text":"Scratchpad directory: `+elsewhere+`/scratchpad"}`+"\n")
	dir, err := runArchiveCLI(t, "--session", "s", "--into", w.into)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "s", "tmp", "scratchpad", "x.txt")); err != nil {
		t.Errorf("the temp dir the transcript names was not archived: %v", err)
	}
}

func TestArchive_IsHiddenAndAbsentFromTheGeneratedReference(t *testing.T) {
	cmd, _, err := newRoot().Find([]string{"archive"})
	if err != nil || !cmd.Hidden {
		t.Fatalf("archive must be a hidden command: %v hidden=%v", err, cmd != nil && cmd.Hidden)
	}
	var help bytes.Buffer
	root := newRoot()
	root.SetOut(&help)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(help.String(), "archive ") {
		t.Errorf("--help lists archive:\n%s", help.String())
	}
	var ref bytes.Buffer
	if err := clidoc.Emit(newRoot(), &ref); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ref.String(), "sr-eval archive") || strings.Contains(ref.String(), "all-sessions") {
		t.Errorf("the generated CLI reference lists archive:\n%s", ref.String())
	}
}
