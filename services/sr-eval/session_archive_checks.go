package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sloprail/sloprail/internal/transcript"
)

// repoOf names the repository a folder belongs to: its main worktree (the
// parent of the git common dir, which every linked worktree shares), or the
// common dir itself for a bare repository. A folder that is gone or not in a
// repository stands for itself.
func repoOf(folder string) string {
	if !isDir(folder) {
		return folder
	}
	out, err := exec.Command("git", "-C", folder, "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return folder
	}
	common := strings.TrimSpace(string(out))
	if common == "" {
		return folder
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(folder, common)
	}
	if r, err := filepath.EvalSymlinks(common); err == nil {
		common = r
	}
	common = filepath.Clean(common)
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	return common
}

// archiveChecks saves `sr-checks log --json` (JSONL) once per repository, not
// once per folder: the verdicts live in the repository's git ref, shared by all
// its worktrees, so a session that tracked many worktrees of one repository
// would otherwise archive the same log many times.
func archiveChecks(m *archiveManifest, folders map[string][]string, dir string) {
	type group struct {
		folders  []string
		sessions map[string]bool
	}
	repos := map[string]*group{}
	for f, sessions := range folders {
		repo := repoOf(f)
		g := repos[repo]
		if g == nil {
			g = &group{sessions: map[string]bool{}}
			repos[repo] = g
		}
		g.folders = append(g.folders, f)
		for _, s := range sessions {
			g.sessions[s] = true
		}
	}
	names := make([]string, 0, len(repos))
	for r := range repos {
		names = append(names, r)
	}
	sort.Strings(names)
	for _, repo := range names {
		g := repos[repo]
		sort.Strings(g.folders)
		sessions := make([]string, 0, len(g.sessions))
		for s := range g.sessions {
			sessions = append(sessions, s)
		}
		sort.Strings(sessions)
		skip := func(reason string) {
			m.Skipped = append(m.Skipped, skippedItem{Item: "checks of " + repo, Reason: reason})
		}
		// Run from one folder that still exists: the main worktree may itself
		// be gone while a linked worktree stands.
		from := ""
		for _, f := range append([]string{repo}, g.folders...) {
			if isDir(f) {
				from = f
				break
			}
		}
		if from == "" {
			skip("the folder is gone")
			continue
		}
		stdout, err := runTool(from, nil, "sr-checks", "log", "--json")
		if err != nil {
			skip(err.Error())
			continue
		}
		rel := filepath.Join("checks", transcript.EncodeProjectDir(repo)+".jsonl")
		if err := os.MkdirAll(filepath.Join(dir, "checks"), 0o755); err != nil {
			skip(err.Error())
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, rel), stdout, 0o644); err != nil {
			skip(err.Error())
			continue
		}
		m.Checks = append(m.Checks, archivedChecks{Repo: repo, File: rel, Folders: g.folders, Sessions: sessions})
	}
}
