package gitrepo

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// reCredentials strips user:pass@ or user@ from https?:// URLs.
	reCredentials = regexp.MustCompile(`^(https?://)([^@]+@)(.+)$`)
	// reSCP converts SCP-style SSH (git@host:path) to a parseable ssh:// URL.
	reSCP = regexp.MustCompile(`^[^@/:]+@([^/:]+):(.+)$`)
)

// NormalizeRemote collapses all git remote URL variants of the same repo to the canonical form
// host/org/repo (lowercase, no scheme, no credentials, no .git suffix, no trailing slash, no default port).
//
// Handles these variants:
// - SSH: git@github.com:org/repo.git → github.com/org/repo
// - HTTPS: https://github.com/org/repo.git → github.com/org/repo
// - ssh:// scheme: ssh://git@github.com/org/repo.git → github.com/org/repo
// - No .git suffix: https://github.com/org/repo → github.com/org/repo
// - Trailing slash: https://github.com/org/repo/ → github.com/org/repo
// - Embedded credentials: https://user:token@github.com/org/repo.git → github.com/org/repo
// - Case: https://github.com/Org/Repo.git → github.com/org/repo
// - Default port stripped: https://github.com:443/org/repo.git → github.com/org/repo
//
// On parse error, returns the input lowercased and stripped of whitespace (best-effort).
func NormalizeRemote(rawURL string) string {
	// Step 1: Trim whitespace and convert to lowercase
	normalized := strings.TrimSpace(rawURL)
	normalized = strings.ToLower(normalized)

	// Step 1b: Local repos (file:// scheme or a bare absolute path) canonicalise to
	// the real filesystem path. Without this, two spellings of the SAME local repo
	// don't match — notably macOS symlinks /var → /private/var, so a file:///var/…
	// resource URL and a /private/var/… cwd origin look like different repos. We
	// resolve symlinks + clean the path (best-effort: an unresolvable path falls
	// back to the cleaned literal). Local remotes are common in dev and tests.
	if p, ok := localRepoPath(normalized); ok {
		// Strip .git BEFORE resolving symlinks: the bare repo dir may be named
		// "<repo>.git" and exist, or ".git" may be a suffix on a path whose parent
		// is what's symlinked — either way, resolving the cleaned, suffix-free path
		// gives one canonical form for both the file:// resource URL and the cwd
		// origin (so /var and /private/var collapse identically).
		p = strings.TrimSuffix(filepath.Clean(p), ".git")
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			p = resolved
		}
		return "file://" + p
	}

	// Step 2: Strip credentials from https:// and http:// URLs
	if m := reCredentials.FindStringSubmatch(normalized); m != nil {
		normalized = m[1] + m[3]
	}

	// Step 3: Convert SCP-style SSH (user@host:path) to ssh://host/path so url.Parse works.
	// Only acts when there is no scheme (no "://").
	if !strings.Contains(normalized, "://") {
		if m := reSCP.FindStringSubmatch(normalized); m != nil {
			normalized = "ssh://" + m[1] + "/" + m[2]
		}
	}

	// Step 4: Parse the URL
	parsed, err := url.Parse(normalized)
	if err != nil {
		// Best-effort: return lowercased, trimmed input
		return strings.TrimSpace(strings.ToLower(rawURL))
	}

	// Step 5: Get hostname (strips port automatically)
	host := parsed.Hostname()
	if host == "" {
		// If no host, try to extract from opaque part or path
		// This can happen with malformed URLs; best-effort fallback
		return strings.TrimSpace(strings.ToLower(rawURL))
	}

	// Step 6: Get path, clean it up
	path := parsed.Path
	// Strip leading slash
	path = strings.TrimPrefix(path, "/")
	// Strip trailing slash
	path = strings.TrimSuffix(path, "/")
	// Strip .git suffix
	path = strings.TrimSuffix(path, ".git")
	// Strip any remaining trailing slash after .git removal
	path = strings.TrimSuffix(path, "/")

	// Step 7: Return canonical form
	if path == "" {
		return host
	}
	return host + "/" + path
}

// localRepoPath reports whether normalized names a LOCAL repository — either a
// file:// URL or a bare absolute filesystem path — and returns its path. It does
// NOT treat scheme'd remote URLs (https://, ssh://, git@host:…) as local.
func localRepoPath(normalized string) (string, bool) {
	if strings.HasPrefix(normalized, "file://") {
		// file:///abs/path → /abs/path ; file://host/path is unusual for git, but
		// url.Parse would put the leading dir in Path, which is fine for matching.
		if u, err := url.Parse(normalized); err == nil && u.Path != "" {
			return u.Path, true
		}
		return strings.TrimPrefix(normalized, "file://"), true
	}
	// A bare absolute path with no scheme (e.g. /tmp/x/repo.git). SCP-style
	// (git@host:path) and scheme'd URLs contain "://" or ":" before any "/", so a
	// leading "/" with no "://" is a local path.
	if strings.HasPrefix(normalized, "/") && !strings.Contains(normalized, "://") {
		return normalized, true
	}
	return "", false
}

// RepoID is the stable identity of a git repository: normalize(origin url) + ":" + its initial
// commit when it has a remote, else the real path of its git common directory (local-only
// repos are told apart by where they live). Every worktree of one repository has the same one.
//
// The remote is origin's, else the first remote's. Two clones of one remote share an ID: a
// caller that must tell clones apart pairs it with CommonDir.
func RepoID(dir string) (string, error) {
	remoteURL, _ := gitOut(dir, "remote", "get-url", "origin")
	if remoteURL == "" {
		if remotes, _ := gitOut(dir, "remote"); remotes != "" {
			remoteURL, _ = gitOut(dir, "remote", "get-url", strings.Split(remotes, "\n")[0])
		}
	}
	if remoteURL != "" {
		initial, err := FirstCommit(dir)
		if err != nil {
			return "", fmt.Errorf("gitrepo: repo id: %w", err)
		}
		return NormalizeRemote(remoteURL) + ":" + initial, nil
	}
	return CommonDir(dir)
}

// FirstCommit is the repository's initial commit (the oldest root when there are several).
func FirstCommit(dir string) (string, error) {
	out, err := gitOut(dir, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		return "", fmt.Errorf("gitrepo: first commit: %w", err)
	}
	lines := strings.Split(out, "\n")
	if out == "" {
		return "", fmt.Errorf("gitrepo: first commit: empty output")
	}
	return lines[len(lines)-1], nil
}

// CommonDir is the real path of the git directory every worktree of dir's repository shares.
func CommonDir(dir string) (string, error) {
	common, err := gitOut(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("gitrepo: common dir: %w", err)
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	if real, err := filepath.EvalSymlinks(common); err == nil {
		return real, nil
	}
	return common, nil
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
