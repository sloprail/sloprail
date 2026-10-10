package main

// `sr-eval archive` records agent session trajectories (Claude Code, Codex, Cursor)
// into a repository-backed archive that does not depend on the session's own
// storage, on the project's repository state, or on origin: the root session
// record, the sub-agents' records where the harness can tie them to it (and an
// explicit note where it cannot), what the harness keeps beside the record
// (Claude Code's tool results, scratchpad and task outputs; Cursor's recorded tool
// outputs), the session's sloprail state store, and the check results of every
// repository the session tracked, all copied.
//
// Everything specific to a harness is asked of it (internal/harness:
// SessionLister, SubagentLocator, SubagentsUnlinkable, CompanionLocator); nothing
// here knows a layout.
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
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/transcript"
	"github.com/sloprail/sloprail/internal/version"
)

// sessionArchiveEnv overrides the default archive location.
const sessionArchiveEnv = "SLOPRAIL_SESSION_ARCHIVE_DIR"

func newArchiveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "archive (--session <id> [--session <id> ...] | --all-sessions) [--into <dir>] [--label <name>] [--harness <name>]",
		Hidden: true,
		Short:  "Archive this project's agent session trajectories",
		Long: `Archive the sessions of the project in the current directory, of the harness
--harness names (default: $` + harness.SelectEnv + `, else Claude Code).

Per session it copies the root transcript, the records of the sub-agents the
harness can tie to it (a harness that cannot, Cursor, is recorded as such in
archive.json, never silently left out), what the harness keeps beside the record
(Claude Code: the session directory's tool results, the scratchpad and task
outputs under its temp root; Cursor: the tool outputs sloprail recorded), and
the session's sloprail state store. It also saves the check results
(sr-checks log --json) of the project repository and of every other repository the
session tracked (once per repository, however many of its worktrees), and the
tracked ranges themselves (sr-session refs list --json), so the archive stands
without git or origin. A repository's log holds every session's verdicts, so only
the ones whose commit lies in a range the archived sessions still tracked are kept
(sr-checks log --range, the range's base as of now: a branch merged since collapses
to its tip, and a verdict only replayed onto the range is not kept); a repository
with no tracked range gets an empty file.

Layout: <into>/<label>/<UTC timestamp>-<rand>/ with archive.json, one directory
per session and checks/. Exactly this entry's directory is committed.

--into defaults to <XDG data dir>/sloprail/session-archives, or
$` + sessionArchiveEnv + `; --label to the project directory's name.

--all-sessions takes the sessions the harness proves to be roots; a record it
cannot prove one (a sub-agent's, on Cursor) is archived only when --session names it.`,
		Args: cobra.NoArgs,
		RunE: runArchive,
	}
	cmd.Flags().StringArray("session", nil, "A session id to archive (repeatable)")
	cmd.Flags().Bool("all-sessions", false, "Archive every root session of the project")
	cmd.Flags().String("into", "", "The archive repository (default: <XDG data dir>/sloprail/session-archives)")
	cmd.Flags().String("label", "", "The archive's group name (default: the project directory's name)")
	cmd.Flags().String("harness", "", "The harness whose sessions to archive (default: $"+harness.SelectEnv+", else "+defaultHarness+")")
	cmd.Flags().Bool("no-commit", false, "Write the entry under --into and commit nothing (--into need not be a repository)")
	return cmd
}

// archiveManifest is archive.json.
type archiveManifest struct {
	Harness   string            `json:"harness"`
	Label     string            `json:"label"`
	Cwd       string            `json:"cwd"`
	Project   string            `json:"project_dir"`
	Sources   archiveSources    `json:"sources"`
	Sessions  []string          `json:"sessions"`
	Subagents map[string]string `json:"subagents"` // session -> what became of its sub-agents
	StartedAt time.Time         `json:"started_at"`
	EndedAt   time.Time         `json:"finished_at"`
	Tools     map[string]string `json:"tool_versions"`
	Checks    []archivedChecks  `json:"checks"`
	Skipped   []skippedItem     `json:"skipped"`
}

type archiveSources struct {
	ProjectDir string `json:"harness_project_dir"`
	// Companions: session -> item -> where the harness kept it, and how it was found.
	Companions map[string]map[string]string `json:"companions,omitempty"`
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
	harnessName, _ := cmd.Flags().GetString("harness")
	noCommit, _ := cmd.Flags().GetBool("no-commit")
	switch {
	case len(ids) == 0 && !all:
		return errors.New("sr-eval archive: name the sessions: --session <id> (repeatable) or --all-sessions")
	case len(ids) > 0 && all:
		return errors.New("sr-eval archive: --session and --all-sessions are exclusive: name the sessions or take all of them")
	}
	h, err := resolveHarness(harnessName, os.Getenv)
	if err != nil {
		return fmt.Errorf("sr-eval archive: %w", err)
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
	out, err := archiveSessions(h, cwd, ids, all, into, label, time.Now(), !noCommit)
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

// archiveSessions archives the sessions of the project at cwd, as harness h keeps them, and
// returns the entry's directory. With commit false the entry is only written: into is a plain
// directory, for a caller that keeps the entry inside an archive of its own (sr-eval run).
func archiveSessions(h harness.Harness, cwd string, ids []string, all bool, into, label string, now time.Time, commit bool) (string, error) {
	t := h.Transcripts()
	lister, ok := t.(harness.SessionLister)
	if !ok {
		return "", fmt.Errorf("sr-eval archive: harness %s cannot list its sessions", h.Name())
	}
	configDir := t.ConfigDir()
	if configDir == "" {
		return "", fmt.Errorf("sr-eval archive: cannot locate %s's configuration directory", h.Name())
	}
	workDir := transcript.ResolveWorkDir(cwd)
	projDir := t.ProjectDir(configDir, workDir)
	records := lister.ProjectSessions(configDir, workDir)
	if len(records) == 0 {
		return "", fmt.Errorf("sr-eval archive: no %s sessions for %s (looked at %s)", h.Name(), cwd, projDir)
	}
	byID := map[string]harness.SessionRecord{}
	for _, r := range records {
		byID[r.ID] = r
	}
	// The default label is the harness's name for the project (Claude Code's and Cursor's
	// encoded project directory; for a harness that keeps no project directory, the folder's own name).
	projName := filepath.Base(t.EncodeProjectDir(workDir))
	if label == "" {
		label = projName
	}
	if strings.ContainsAny(label, `/\`) || label == "." || label == ".." {
		return "", fmt.Errorf("sr-eval archive: --label %q must be a plain name", label)
	}
	if all {
		for _, r := range records {
			if r.Root {
				ids = append(ids, r.ID)
			}
		}
		sort.Strings(ids)
		if len(ids) == 0 {
			return "", fmt.Errorf("sr-eval archive: %s holds no root session of %s", projDir, h.Name())
		}
	}
	for _, id := range ids {
		if id == "" || strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
			return "", fmt.Errorf("sr-eval archive: %q is not a session id", id)
		}
		if _, ok := byID[id]; !ok {
			return "", fmt.Errorf("sr-eval archive: session %s has no transcript in %s", id, projDir)
		}
	}

	if commit {
		if err := ensureArchiveRepo(into); err != nil {
			return "", err
		}
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
		Harness: h.Name(), Label: label, Cwd: cwd, Project: projName, Sessions: ids, StartedAt: now.UTC(),
		Sources:   archiveSources{ProjectDir: projDir, Companions: map[string]map[string]string{}},
		Subagents: map[string]string{},
	}
	folders := map[string][]string{}      // tracked folder -> sessions that tracked it
	ranges := map[string][]trackedRange{} // tracked folder -> the ranges the sessions held there
	if root, err := gitrepo.Root(cwd); err == nil && root != "" {
		folders[filepath.Clean(root)] = nil
	}
	for _, id := range ids {
		for _, r := range archiveOneSession(m, h, cwd, byID[id], dir) {
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
	if !commit {
		return dir, nil
	}
	if err := commitDir(into, dir, fmt.Sprintf("session-archive: %s (%d sessions)", label, len(ids))); err != nil {
		return "", err
	}
	return dir, nil
}

// archiveOneSession copies what one session left behind into <dir>/<id>/ and
// returns the ranges it tracked. A missing piece is recorded in
// the manifest, never an error: a session may have no sub-agents, no companions,
// or no state at all.
func archiveOneSession(m *archiveManifest, h harness.Harness, cwd string, rec harness.SessionRecord, dir string) []trackedRange {
	id, tpath := rec.ID, rec.Path
	skip := func(item, reason string) {
		m.Skipped = append(m.Skipped, skippedItem{Session: id, Item: item, Reason: reason})
	}
	sdir := filepath.Join(dir, id)
	if err := copyFile(tpath, filepath.Join(sdir, "transcript.jsonl")); err != nil {
		skip("transcript", err.Error())
	}
	copyDir := func(src, dst, item string, exclude ...string) {
		if !isDir(src) {
			skip(item, "absent: "+src)
			return
		}
		if r, rerr := filepath.EvalSymlinks(src); rerr == nil {
			src = r // WalkDir does not descend a symlinked root
		}
		skipped, err := copyTreeLenient(src, filepath.Join(sdir, dst), exclude...)
		if err != nil {
			skip(item, err.Error())
		}
		for _, s := range skipped {
			skip(item+"/"+s, "not copied")
		}
	}

	archiveSubagents(m, h, rec, sdir, skip)

	// What the harness keeps beside the record.
	if l, ok := h.Transcripts().(harness.CompanionLocator); ok {
		for _, c := range l.Companions(tpath) {
			switch {
			case c.Path == "":
				skip(c.Item, c.Why)
				continue
			case isDir(c.Path):
				copyDir(c.Path, c.Dir, c.Item, c.Exclude...)
			default:
				if _, err := os.Stat(c.Path); err != nil {
					skip(c.Item, "absent: "+c.Path)
					continue
				}
				if err := copyFile(c.Path, filepath.Join(sdir, c.Dir, filepath.Base(c.Path))); err != nil {
					skip(c.Item, err.Error())
				}
			}
			if c.How != "" {
				if m.Sources.Companions[id] == nil {
					m.Sources.Companions[id] = map[string]string{}
				}
				m.Sources.Companions[id][c.Item] = c.Path + " (" + c.How + ")"
			}
		}
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

	return archiveRefs(h, cwd, tpath, id, sdir, skip)
}

// archiveSubagents copies the records of the sub-agents the harness can tie to the session
// into <sdir>/subagents/, and records in the manifest what became of them: how many, none, or
// that the harness declares a sub-agent's session cannot be tied to its parent (so none can be
// archived with this one, and the archive says so rather than looking complete).
func archiveSubagents(m *archiveManifest, h harness.Harness, rec harness.SessionRecord, sdir string, skip func(item, reason string)) {
	t := h.Transcripts()
	if u, ok := t.(harness.SubagentsUnlinkable); ok && u.SubagentsUnlinkable() {
		m.Subagents[rec.ID] = "unlinkable: " + h.Name() + " names no parent in a sub-agent's record, so its sub-agents are not archived with the session"
		return
	}
	l, ok := t.(harness.SubagentLocator)
	if !ok {
		m.Subagents[rec.ID] = "unlinkable: " + h.Name() + " has no way to find a session's sub-agents"
		return
	}
	n := 0
	for _, f := range l.SubagentFiles(rec.Path) {
		if err := copyFile(f.Path, filepath.Join(sdir, "subagents", f.Rel)); err != nil {
			skip("subagents/"+f.Rel, err.Error())
			continue
		}
		n++
	}
	if n == 0 {
		m.Subagents[rec.ID] = "none"
		return
	}
	m.Subagents[rec.ID] = fmt.Sprintf("archived: %d files", n)
}

// archiveRefs saves `sr-session refs list --json` for the session, run as the
// session itself (a hook-style payload on stdin names it, parsed as harness h's),
// and returns the ranges it lists.
func archiveRefs(h harness.Harness, cwd, tpath, id, sdir string, skip func(item, reason string)) []trackedRange {
	payload, _ := json.Marshal(map[string]string{"session_id": id, "transcript_path": tpath, "cwd": cwd})
	stdout, err := runTool(cwd, bytes.NewReader(payload), []string{harness.SelectEnv + "=" + h.Name()}, "sr-session", "refs", "list", "--json")
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
// talk to each other, and returns its stdout; a failure carries its stderr. extraEnv
// (KEY=VALUE) is added to the process's environment.
func runTool(dir string, stdin *bytes.Reader, extraEnv []string, name string, args ...string) ([]byte, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("%s is not on PATH", name)
	}
	c := exec.Command(path, args...)
	c.Dir = dir
	if stdin != nil {
		c.Stdin = stdin
	}
	if len(extraEnv) > 0 {
		c.Env = append(os.Environ(), extraEnv...)
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
		if out, err := runTool("", nil, nil, name, "--version"); err == nil {
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
