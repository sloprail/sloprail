// This file is the sr-mark binary's OWN marker-writing logic. It is PURE GO — no store, no cgo —
// because impl markers are a GENERAL annotation concept, not guardrail-specific: the standalone
// `sr-mark` binary uses this, and it may serve non-guardrail annotation KINDS too (the `sr:`
// namespace — `sr:blueprint` is just the first kind). Nothing imports this as a package — other
// services invoke the `sr-mark` BINARY as a subprocess.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// commentLeaders is the EXPLICIT whitelist of supported impl languages → their single-line
// comment leader. It is the SINGLE source of truth: a marker on a file whose extension is
// absent is a hard error (fail-fast — never guess a leader). ".md" uses "#" too, but ONLY
// inside the file's YAML front matter block — see WriteMarker's front-matter guard below.
var commentLeaders = map[string]string{
	".ts": "//", ".tsx": "//", ".js": "//", ".jsx": "//",
	".go": "//", ".java": "//", ".rs": "//", ".kt": "//",
	".cs": "//", ".scala": "//", ".php": "//", ".c": "//",
	".cc": "//", ".cpp": "//", ".h": "//", ".hpp": "//", ".swift": "//",
	".py": "#", ".rb": "#", ".sh": "#", ".bash": "#", ".yaml": "#", ".yml": "#",
	".md":  "#",
	".sql": "--",
}

// CommentLeader returns the single-line comment leader for path's language, or an error when
// the extension is not in the whitelist.
func CommentLeader(path string) (string, error) {
	ext := strings.ToLower(extOf(path))
	if leader, ok := commentLeaders[ext]; ok {
		return leader, nil
	}
	return "", fmt.Errorf("marker: unsupported file extension %q for %s — no comment leader (supported: %s)",
		ext, path, supportedExts())
}

// supportedExts returns the whitelisted extensions, sorted, for error messages.
func supportedExts() string {
	exts := make([]string, 0, len(commentLeaders))
	for e := range commentLeaders {
		exts = append(exts, e)
	}
	sort.Strings(exts)
	return strings.Join(exts, " ")
}

// WriteMarker inserts a `<leader> sr:<kind> <fqn>` comment at `line` (1-based) of the file at
// absPath, matching the file's language. `kind` is the marker namespace member (e.g.
// "blueprint"); the emitted comment is always `sr:<kind> <fqn>`, so a new marker kind is just
// a different `kind` here. Idempotent: if the comment is already on the line above (or already
// present on the target line) it is a no-op. The comment inherits the target line's indentation.
// Fails loudly for an unsupported language. For ".md", `line` must fall inside the file's YAML
// front matter block (between the opening and closing `---` delimiters) — markdown has no
// general-purpose comment syntax, so "#" is only unambiguous there; a target line outside front
// matter is a hard error rather than silently emitting a stray markdown heading.
// sr:invariant cli/mark-apply-is-idempotent
func WriteMarker(absPath, kind, fqn string, line int) error {
	leader, err := CommentLeader(absPath)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("marker: read %s: %w", absPath, err)
	}
	lines := strings.Split(string(b), "\n")
	if strings.ToLower(extOf(absPath)) == ".md" {
		if err := requireInFrontMatter(lines, line, absPath); err != nil {
			return err
		}
	}
	comment := leader + " sr:" + kind + " " + fqn
	idx := line - 1
	if idx < 0 {
		idx = 0
	}
	if idx > len(lines) {
		idx = len(lines)
	}
	if idx > 0 && strings.TrimSpace(lines[idx-1]) == comment {
		return nil
	}
	if idx < len(lines) && strings.Contains(lines[idx], comment) {
		return nil
	}
	indent := ""
	if idx < len(lines) {
		tgt := lines[idx]
		indent = tgt[:len(tgt)-len(strings.TrimLeft(tgt, " \t"))]
	}
	lines = append(lines[:idx], append([]string{indent + comment}, lines[idx:]...)...)
	if err := os.WriteFile(absPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return fmt.Errorf("marker: write into %s: %w", absPath, err)
	}
	return nil
}

// extOf returns the lowercased extension of a path (including the dot), or "".
func extOf(p string) string {
	for i := len(p) - 1; i >= 0 && p[i] != '/'; i-- {
		if p[i] == '.' {
			return p[i:]
		}
	}
	return ""
}

// requireInFrontMatter errors unless `line` (1-based) falls strictly between the opening `---`
// (line 1) and its matching closing `---` delimiter of a YAML front matter block. A markdown
// file with no front matter block at all, or a target line outside it, is a hard error — mirrors
// CommentLeader's fail-fast-never-guess stance for unsupported extensions.
func requireInFrontMatter(lines []string, line int, absPath string) error {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return fmt.Errorf("marker: %s has no YAML front matter block (must start with '---') — markdown markers are only supported inside front matter", absPath)
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return fmt.Errorf("marker: %s front matter block is not closed with a second '---'", absPath)
	}
	idx := line - 1
	if idx <= 0 || idx >= end {
		return fmt.Errorf("marker: %s:%d is outside the front matter block (lines 2-%d) — markdown markers must target a line inside front matter", absPath, line, end)
	}
	return nil
}

// DeleteMarkers walks root and removes every line that IS a `// sr:<kind> <fqn>` (or `# …` for
// python/sh/yaml/markdown-front-matter, or `-- …` for SQL) marker comment, for any fqn in fqns —
// mirroring the doc comment leaders WriteMarker uses. Only lines that consist SOLELY of the
// marker comment (plus leading whitespace) are removed, matching how sr-mark writes markers
// (own line, no trailing code) — a marker sharing a line with real code is left alone rather than
// risk deleting logic. Returns the number of marker lines removed. Skips node_modules/.git.
// Also skips any file excluded by the repo's .gitignore (best-effort — outside a git repo every
// file is walked, unfiltered).
func DeleteMarkers(root, kind string, fqns []string) (int, error) {
	if len(fqns) == 0 {
		return 0, nil
	}
	fqnSet := map[string]bool{}
	for _, f := range fqns {
		fqnSet[f] = true
	}
	// One line-leader alternation covers every supported comment style; the fqn itself is
	// matched against the fqnSet after extraction (a single compiled regex per kind, not one
	// per fqn, keeps this a single tree walk regardless of how many fqns are deleted).
	pat := regexp.MustCompile(`^\s*(?://|#|--)\s*sr:` + regexp.QuoteMeta(kind) + `\s+(\S+)\s*$`)

	nonIgnored, haveGit := nonIgnoredFiles(context.Background(), root)
	removed := 0
	walkErr := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if fi.IsDir() {
			if fi.Name() == "node_modules" || fi.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if haveGit && relErr == nil && !nonIgnored[filepath.ToSlash(rel)] {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		lines := strings.Split(string(b), "\n")
		var kept []string
		changed := false
		for _, line := range lines {
			if m := pat.FindStringSubmatch(line); m != nil && fqnSet[m[1]] {
				changed = true
				removed++
				continue
			}
			kept = append(kept, line)
		}
		if changed {
			if err := os.WriteFile(p, []byte(strings.Join(kept, "\n")), fi.Mode()); err != nil {
				return fmt.Errorf("marker: rewrite %s: %w", p, err)
			}
		}
		return nil
	})
	if walkErr != nil {
		return removed, walkErr
	}
	return removed, nil
}

// nonIgnoredFiles returns the set of repo-relative paths under dir that git would show you —
// tracked files plus untracked-but-not-ignored ones — via `git ls-files --cached --others
// --exclude-standard`, which is the canonical way to enumerate a working tree while respecting
// .gitignore (root .gitignore, nested .gitignore, .git/info/exclude, and global excludes all
// apply, matching what `git status` itself would show as ignored). Paths are '/'-separated,
// dir-relative, matching filepath.Rel(dir, ...) on any OS.
//
// Returns (nil, false) when dir is not a git repository (or git is unavailable) — the caller's
// signal to fall back to scanning everything, since there is no .gitignore to respect outside a
// repo. This is a deliberate best-effort: a walker that ALWAYS filtered by this set would silently
// scan nothing for a caller that passes a plain temp directory in a test.
func nonIgnoredFiles(ctx context.Context, dir string) (map[string]bool, bool) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "ls-files", "--cached", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		return nil, false
	}
	set := map[string]bool{}
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			set[p] = true
		}
	}
	return set, true
}
