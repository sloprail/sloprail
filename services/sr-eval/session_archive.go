package main

// `sr-eval archive` records Claude Code session trajectories into a git-backed
// archive that does not depend on the session's own storage, on the project's
// git state, or on origin: the transcripts, the session's scratchpad and task
// outputs, its sloprail state store, and the check results of every repository
// the session tracked, all copied.
//
// Hidden: an operator's tool (like `sr-checks log`), not part of an agent's
// workflow, so it stays out of --help, the generated CLI reference and the docs.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/transcript"
	"github.com/sloprail/sloprail/internal/version"
)

// sessionArchiveEnv overrides the default archive location.
const sessionArchiveEnv = "SLOPRAIL_SESSION_ARCHIVE_DIR"

func newArchiveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "archive (--session <id> [--session <id> ...] | --all-sessions) [--into <dir>] [--label <name>]",
		Hidden: true,
		Short:  "Archive this project's Claude Code session trajectories",
		Long: `Archive Claude Code sessions of the project in the current directory.

Per session it copies the transcript and its session directory (subagents,
tool-results), the scratchpad and task outputs Claude Code keeps under its temp
root, and the session's sloprail state store. It also saves the check results
(sr-checks log --json) of the project repository and of every other repository the
session tracked (once per repository, however many of its worktrees), and the tracked ranges themselves (sr-session refs list --json),
so the archive stands without git or origin. A repository's log holds every session's
verdicts, so only the ones whose commit lies in a range the archived sessions tracked are
kept (sr-checks log --range); a repository with no tracked range gets an empty file.

Layout: <into>/<label>/<UTC timestamp>-<rand>/ with archive.json, one directory
per session and checks/. Exactly this entry's directory is committed.

--into defaults to <XDG data dir>/sloprail/session-archives, or
$` + sessionArchiveEnv + `; --label to the project directory's name.`,
		Args: cobra.NoArgs,
		RunE: runArchive,
	}
	cmd.Flags().StringArray("session", nil, "A session id to archive (repeatable)")
	cmd.Flags().Bool("all-sessions", false, "Archive every session of the project")
	cmd.Flags().String("into", "", "The archive repository (default: <XDG data dir>/sloprail/session-archives)")
	cmd.Flags().String("label", "", "The archive's group name (default: the Claude Code project directory's name)")
	return cmd
}

// archiveManifest is archive.json.
type archiveManifest struct {
	Label     string            `json:"label"`
	Cwd       string            `json:"cwd"`
	Project   string            `json:"project_dir"`
	Sources   archiveSources    `json:"sources"`
	Sessions  []string          `json:"sessions"`
	StartedAt time.Time         `json:"started_at"`
	EndedAt   time.Time         `json:"finished_at"`
	Tools     map[string]string `json:"tool_versions"`
	Checks    []archivedChecks  `json:"checks"`
	Skipped   []skippedItem     `json:"skipped"`
}

type archiveSources struct {
	ProjectDir string            `json:"claude_project_dir"`
	Scratchpad map[string]string `json:"scratchpad_roots,omitempty"` // session -> where its temp dir was found, and how
}

// archivedChecks is one repository's check log. The verdicts live in the
// repository's own ref, shared by all its worktrees, so one file serves every
// tracked folder that maps to the repository.
type archivedChecks struct {
	Repo     string   `json:"repo"`    // the main worktree (or the common dir of a bare repository)
	File     string   `json:"file"`    // checks/<repo slug>.jsonl
	Folders  []string `json:"folders"` // every tracked folder of the repository
	Sessions []string `json:"tracked_by_sessions"`
}

type skippedItem struct {
	Session string `json:"session,omitempty"`
	Item    string `json:"item"`
	Reason  string `json:"reason"`
}

func runArchive(cmd *cobra.Command, _ []string) error {
	ids, _ := cmd.Flags().GetStringArray("session")
	all, _ := cmd.Flags().GetBool("all-sessions")
	into, _ := cmd.Flags().GetString("into")
	label, _ := cmd.Flags().GetString("label")
	switch {
	case len(ids) == 0 && !all:
		return errors.New("sr-eval archive: name the sessions: --session <id> (repeatable) or --all-sessions")
	case len(ids) > 0 && all:
		return errors.New("sr-eval archive: --session and --all-sessions are exclusive: name the sessions or take all of them")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("sr-eval archive: working directory: %w", err)
	}
	if into == "" {
		if into, err = sessionArchiveRoot(); err != nil {
			return err
		}
	}
	out, err := archiveSessions(cwd, ids, all, into, label, time.Now())
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), out)
	return nil
}

// sessionArchiveRoot is <XDG data dir>/sloprail/session-archives, or the env override.
func sessionArchiveRoot() (string, error) {
	if dir := os.Getenv(sessionArchiveEnv); dir != "" {
		return dir, nil
	}
	home, err := dataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, AppName, "session-archives"), nil
}

// archiveSessions archives the sessions of the project at cwd and returns the entry's directory.
func archiveSessions(cwd string, ids []string, all bool, into, label string, now time.Time) (string, error) {
	configDir := transcript.ConfigDir()
	projDir := transcript.ProjectDir(configDir, cwd)
	if projDir == "" {
		return "", errors.New("sr-eval archive: cannot locate Claude Code's configuration directory")
	}
	if !isDir(projDir) {
		return "", fmt.Errorf("sr-eval archive: no Claude Code project directory for %s (looked at %s)", cwd, projDir)
	}
	projName := filepath.Base(projDir)
	if label == "" {
		label = projName
	}
	if strings.ContainsAny(label, `/\`) || label == "." || label == ".." {
		return "", fmt.Errorf("sr-eval archive: --label %q must be a plain name", label)
	}
	if all {
		matches, _ := filepath.Glob(filepath.Join(projDir, "*.jsonl"))
		for _, m := range matches {
			ids = append(ids, strings.TrimSuffix(filepath.Base(m), ".jsonl"))
		}
		sort.Strings(ids)
		if len(ids) == 0 {
			return "", fmt.Errorf("sr-eval archive: %s holds no session", projDir)
		}
	}
	for _, id := range ids {
		if id == "" || strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
			return "", fmt.Errorf("sr-eval archive: %q is not a session id", id)
		}
		if _, err := os.Stat(filepath.Join(projDir, id+".jsonl")); err != nil {
			return "", fmt.Errorf("sr-eval archive: session %s has no transcript in %s", id, projDir)
		}
	}

	if err := ensureArchiveRepo(into); err != nil {
		return "", err
	}
	entry, err := runID(now)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(into, label, entry)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("sr-eval archive: create %s: %w", dir, err)
	}

	m := &archiveManifest{
		Label: label, Cwd: cwd, Project: projName, Sessions: ids, StartedAt: now.UTC(),
		Sources: archiveSources{ProjectDir: projDir, Scratchpad: map[string]string{}},
	}
	folders := map[string][]string{}      // tracked folder -> sessions that tracked it
	ranges := map[string][]trackedRange{} // tracked folder -> the ranges the sessions held there
	if root, err := gitrepo.Root(cwd); err == nil && root != "" {
		folders[filepath.Clean(root)] = nil
	}
	for _, id := range ids {
		for _, r := range archiveOneSession(m, cwd, projDir, projName, id, dir) {
			f := filepath.Clean(r.Folder)
			if !contains(folders[f], id) {
				folders[f] = append(folders[f], id)
			}
			ranges[f] = append(ranges[f], r)
		}
	}
	archiveChecks(m, folders, ranges, dir)
	m.Tools = toolVersions()
	m.EndedAt = time.Now().UTC()

	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", fmt.Errorf("sr-eval archive: encode archive.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "archive.json"), body, 0o644); err != nil {
		return "", fmt.Errorf("sr-eval archive: write archive.json: %w", err)
	}
	if err := commitDir(into, dir, fmt.Sprintf("session-archive: %s (%d sessions)", label, len(ids))); err != nil {
		return "", err
	}
	return dir, nil
}

// archiveOneSession copies what one session left behind into <dir>/<id>/ and
// returns the ranges it tracked. A missing piece is recorded in
// the manifest, never an error: a session may have no subagents, no scratchpad,
// or no state at all.
func archiveOneSession(m *archiveManifest, cwd, projDir, projName, id, dir string) []trackedRange {
	skip := func(item, reason string) {
		m.Skipped = append(m.Skipped, skippedItem{Session: id, Item: item, Reason: reason})
	}
	sdir := filepath.Join(dir, id)
	tpath := filepath.Join(projDir, id+".jsonl")
	if err := copyFile(tpath, filepath.Join(sdir, "transcript.jsonl")); err != nil {
		skip("transcript", err.Error())
	}
	copyDir := func(src, dst, item string) {
		if !isDir(src) {
			skip(item, "absent: "+src)
			return
		}
		if r, rerr := filepath.EvalSymlinks(src); rerr == nil {
			src = r // WalkDir does not descend a symlinked root
		}
		skipped, err := copyTreeLenient(src, filepath.Join(sdir, dst))
		if err != nil {
			skip(item, err.Error())
		}
		for _, s := range skipped {
			skip(item+"/"+s, "not copied")
		}
	}
	copyDir(filepath.Join(projDir, id), "session-dir", "session dir (subagents, tool-results)")

	// The scratchpad and task outputs: <tmp>/claude-<uid>/<project dir>/<session>/.
	if root, how := findTempDir(projName, id, tpath); root != "" {
		m.Sources.Scratchpad[id] = root + " (" + how + ")"
		copyDir(root, "tmp", "scratchpad and tasks")
	} else {
		skip("scratchpad and tasks", "no temp directory found for the session")
	}

	// The state store, keyed by the session's stable identity (the origin
	// record's uuid), which is not always the transcript's filename.
	stateIDs := []string{id}
	if ident, err := sessionpath.StableIdentity(tpath, cwd); err == nil && ident.ID != "" && ident.ID != id {
		stateIDs = append(stateIDs, ident.ID)
	}
	stateFound := false
	for _, sid := range stateIDs {
		db, err := sessionpath.StateDB(cwd, sid)
		if err != nil {
			continue
		}
		if isDir(filepath.Dir(db)) {
			stateFound = true
			copyDir(filepath.Dir(db), filepath.Join("state", sid), "sloprail state store")
		}
	}
	if !stateFound {
		skip("sloprail state store", "absent: no state directory for "+strings.Join(stateIDs, ", "))
	}

	return archiveRefs(cwd, tpath, id, sdir, skip)
}

// archiveRefs saves `sr-session refs list --json` for the session, run as the
// session itself (a hook-style payload on stdin names it), and returns the
// ranges it lists.
func archiveRefs(cwd, tpath, id, sdir string, skip func(item, reason string)) []trackedRange {
	payload, _ := json.Marshal(map[string]string{"session_id": id, "transcript_path": tpath, "cwd": cwd})
	stdout, err := runTool(cwd, bytes.NewReader(payload), "sr-session", "refs", "list", "--json")
	if err != nil {
		skip("refs list --json", err.Error())
		return nil
	}
	if err := os.MkdirAll(sdir, 0o755); err == nil {
		err = os.WriteFile(filepath.Join(sdir, "refs.json"), stdout, 0o644)
		if err != nil {
			skip("refs list --json", err.Error())
		}
	}
	var rows []trackedRange
	if err := json.Unmarshal(stdout, &rows); err != nil {
		skip("refs list --json", "unreadable output: "+err.Error())
		return nil
	}
	var ranges []trackedRange
	for _, r := range rows {
		if r.Folder != "" {
			ranges = append(ranges, r)
		}
	}
	return ranges
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// runTool execs a sibling binary from PATH in dir, the way sloprail's binaries
// talk to each other, and returns its stdout; a failure carries its stderr.
func runTool(dir string, stdin *bytes.Reader, name string, args ...string) ([]byte, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("%s is not on PATH", name)
	}
	c := exec.Command(path, args...)
	c.Dir = dir
	if stdin != nil {
		c.Stdin = stdin
	}
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func toolVersions() map[string]string {
	v := map[string]string{"sr-eval": version.Version}
	for _, name := range []string{"sr-session", "sr-checks"} {
		if out, err := runTool("", nil, name, "--version"); err == nil {
			v[name] = strings.TrimSpace(string(out))
		} else {
			v[name] = "unavailable"
		}
	}
	if out, err := exec.Command("git", "--version").Output(); err == nil {
		v["git"] = strings.TrimSpace(string(out))
	}
	return v
}

// findTempDir locates Claude Code's per-session temp directory,
// <tmp root>/claude-<uid>/<project dir>/<session>/ (it holds scratchpad/ and
// tasks/). The transcript is asked first, for it may name the exact path; then
// $CLAUDE_CODE_TMPDIR (Claude Code's own override), then the platform's temp
// dirs. Symlinked roots (macOS /tmp -> /private/tmp) are resolved.
func findTempDir(projName, id, transcriptPath string) (dir, how string) {
	re := regexp.MustCompile(`(/[^\s"'\\]*/claude-[0-9]+/[A-Za-z0-9-]+/` + regexp.QuoteMeta(id) + `)/`)
	if p := scanForTempDir(transcriptPath, re); p != "" {
		return p, "named in the transcript"
	}
	var bases []string
	if d := os.Getenv("CLAUDE_CODE_TMPDIR"); d != "" {
		bases = append(bases, d)
	}
	bases = append(bases, "/tmp", os.TempDir())
	for _, base := range bases {
		cand := filepath.Join(base, fmt.Sprintf("claude-%d", os.Getuid()), projName, id)
		if isDir(cand) {
			if r, err := filepath.EvalSymlinks(cand); err == nil {
				cand = r
			}
			return cand, "temp root " + base
		}
	}
	return "", ""
}

// scanForTempDir streams the transcript (these run to hundreds of MB; reading
// one whole would double the process's memory) in chunks that overlap enough
// to keep a path split across a chunk boundary, and returns the first match
// that is a directory.
func scanForTempDir(path string, re *regexp.Regexp) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	const chunk, overlap = 4 << 20, 4096
	buf := make([]byte, 0, chunk+overlap)
	tmp := make([]byte, chunk)
	for {
		n, rerr := f.Read(tmp)
		buf = append(buf, tmp[:n]...)
		for _, m := range re.FindAllSubmatch(buf, -1) {
			if isDir(string(m[1])) {
				return string(m[1])
			}
		}
		if len(buf) > overlap {
			buf = append(buf[:0], buf[len(buf)-overlap:]...)
		}
		if rerr != nil {
			return ""
		}
	}
}
