package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sloprail/sloprail/internal/gitrepo"
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

// trackedRange is one row of `sr-session refs list --json`: the range a session
// answered for in a folder. Base is the one the range is judged from now; Head is
// a branch or a commit; HeadSHA and FirstTip are the commits the head pointed at.
type trackedRange struct {
	Folder, Base, Head, HeadSHA, FirstTip string
}

// revCommit resolves rev to a commit in repo, "" when it names none.
func revCommit(repo, rev string) string {
	if rev == "" || strings.HasPrefix(rev, "-") {
		return ""
	}
	out, err := exec.Command("git", "-C", repo, "rev-parse", "--verify", "-q", rev+"^{commit}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// rangeArgs are the --range values of sr-checks log for the ranges: one per distinct
// head the range ever pointed at (its tip when first tracked, the commit it last
// pointed at, and where its branch stands now), each from the range's base. A range
// whose base or heads no longer resolve is left out and named in the second result.
func rangeArgs(repo string, ranges []trackedRange) (args, unresolved []string) {
	seen := map[string]bool{}
	for _, r := range ranges {
		base := r.Base
		if base != gitrepo.EmptyTree {
			if base = revCommit(repo, base); base == "" {
				unresolved = append(unresolved, r.Folder+": base "+r.Base)
				continue
			}
		}
		heads := map[string]bool{}
		for _, h := range []string{r.HeadSHA, r.FirstTip, r.Head} {
			if strings.HasPrefix(h, "detached/") {
				continue
			}
			if sha := revCommit(repo, h); sha != "" {
				heads[sha] = true
			}
		}
		if len(heads) == 0 {
			unresolved = append(unresolved, r.Folder+": head "+r.Head)
		}
		for h := range heads {
			if a := base + ".." + h; !seen[a] {
				seen[a] = true
				args = append(args, a)
			}
		}
	}
	sort.Strings(args)
	return args, unresolved
}

// archiveChecks saves `sr-checks log --json` (JSONL) once per repository, not
// once per folder: the verdicts live in the repository's git ref, shared by all
// its worktrees, so a session that tracked many worktrees of one repository
// would otherwise archive the same log many times. That ref also holds the
// verdicts of every other session and branch of the repository, so the file is
// cut to the verdicts whose commit lies in a range the archived sessions tracked.
func archiveChecks(m *archiveManifest, folders map[string][]string, ranges map[string][]trackedRange, dir string) {
	type group struct {
		folders  []string
		sessions map[string]bool
		ranges   []trackedRange
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
		g.ranges = append(g.ranges, ranges[f]...)
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
		// repo is the main worktree (or common dir), which exists whenever any of
		// its worktrees does; if the main worktree is gone, git cannot resolve the
		// common dir and each standing worktree stands for itself.
		if !isDir(repo) {
			skip("the folder is gone")
			continue
		}
		args, unresolved := rangeArgs(repo, g.ranges)
		for _, u := range unresolved {
			skip("a tracked range is not in the repository, so its verdicts are not kept: " + u)
		}
		// No range, no verdict this session relied on: the file stays, empty.
		stdout := []byte{}
		if len(args) > 0 {
			cmdArgs := []string{"log", "--json"}
			for _, a := range args {
				cmdArgs = append(cmdArgs, "--range", a)
			}
			var err error
			if stdout, err = runTool(repo, nil, "sr-checks", cmdArgs...); err != nil {
				skip(err.Error())
				continue
			}
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
