package main

// archivekit.go is what every sr-eval archive shares: a git repository the
// entries are committed to, a collision-proof entry id, the copy helpers, and
// the commit of exactly one entry's own directory. The eval-run archive
// (archive.go) and the session-trajectory archive (session_archive.go) are two
// users of it, so neither can drift from the other on how an entry is named,
// copied or committed.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// dataHome mirrors services/sr-session/statedir.go's own — the two packages
// cannot import each other (separate `package main`s; see go.mod's own
// reasoning: what passes between these binaries is exec, not import), so the
// small, stable part (the cross-platform XDG lookup) is duplicated rather than
// promoted to a shared internal/ package for one four-branch function.
func dataHome() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support"), nil
	case "windows":
		if dir := os.Getenv("LocalAppData"); dir != "" {
			return dir, nil
		}
		return filepath.Join(home, "AppData", "Local"), nil
	default:
		return filepath.Join(home, ".local", "share"), nil
	}
}

// ensureArchiveRepo makes sure root exists and is a git repository, creating
// both on first use. A plain directory of run folders would still be
// browsable, but git is what turns it into a history: `git log`, `git diff`
// between two runs' transcripts, `git blame` on when a fixture started
// failing — ordinary tools, no bespoke index to maintain.
func ensureArchiveRepo(root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create archive root %s: %w", root, err)
	}
	if isDir(filepath.Join(root, ".git")) {
		return nil
	}
	init := exec.Command("git", "-C", root, "init", "--quiet")
	if out, err := init.CombinedOutput(); err != nil {
		return fmt.Errorf("git init %s: %w: %s", root, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// runID names one run's directory under <root>/<fixture>/: a sortable
// timestamp (so `ls` and `git log --oneline -- <fixture>` already read in
// run order) plus a short random suffix (so two runs started in the same
// second, e.g. two models run back to back by a script, never collide).
func runID(now time.Time) (string, error) {
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("generate run id: %w", err)
	}
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(suffix), nil
}

// hasGitIdentity reports whether `git -C root config user.email` resolves to
// anything — local, global, or system config all count, since `git commit`
// itself does not distinguish them. Checked once per commit rather than
// cached: this runs once per `sr-eval run`, not in a hot loop, and a cache
// would risk holding a stale answer across a run where the operator fixes
// their config mid-session.
func hasGitIdentity(root string) bool {
	out, err := exec.Command("git", "-C", root, "config", "user.email").Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// copyTree recursively copies src into dst.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// commitDir stages and commits exactly this archive entry's OWN directory — not `git
// add -A` over the whole archive, and not even the fixture's whole
// directory, so a run started while a SIBLING run for the same fixture is
// still being written (two models run back to back by a script) can never
// stage or commit that other run's half-written files.
//
// The repo's OWN configured identity and signing are used as-is — an
// operator who `git init`s this archive themselves and sets it up to match
// their own commits (name, email, commit.gpgsign, signingkey) elsewhere gets
// exactly that here too, sr-eval commits indistinguishable from their own.
// The placeholder identity below is a FALLBACK, not a default: only reached
// when the repo has no user.name/user.email configured anywhere (global or
// local) and `git commit` would otherwise refuse outright with "Author
// identity unknown" — a fresh archive on a fresh machine, before anyone has
// set it up. hasGitIdentity distinguishes the two cases; --no-gpg-sign is
// scoped to that same fallback path, so an operator's own signing
// configuration is never silently overridden.
func commitDir(root, runDir, msg string) error {
	relDir, err := filepath.Rel(root, runDir)
	if err != nil {
		return fmt.Errorf("resolve run dir relative to archive root: %w", err)
	}
	add := exec.Command("git", "-C", root, "add", "--", relDir)
	if out, err := add.CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %w: %s", err, strings.TrimSpace(string(out)))
	}

	args := []string{"-C", root}
	if !hasGitIdentity(root) {
		args = append(args,
			"-c", "user.name=sr-eval",
			"-c", "user.email=sr-eval@localhost",
		)
	}
	// The pathspec after "--" commits only this entry's directory, even if
	// something else in the archive repo was staged by hand.
	args = append(args, "commit", "--quiet", "-m", msg)
	if !hasGitIdentity(root) {
		// Only the placeholder identity's commits are forced unsigned — it
		// has no key to sign with, and letting `commit.gpgsign` (inherited
		// from a global config the archive itself never set) apply to it
		// would fail the commit outright over a key that names an identity
		// this fallback never claims to be.
		args = append(args, "--no-gpg-sign")
	}

	args = append(args, "--", relDir)
	commit := exec.Command("git", args...)
	out, err := commit.CombinedOutput()
	if err != nil {
		// "nothing to commit" is not a real failure here: it means THIS
		// exact run (same fixture, same run id, same content) was already
		// archived, which only happens if archiveRun is called twice for
		// one run. Surfacing it as an error would make a caller's retry
		// logic worse, not safer.
		if strings.Contains(string(out), "nothing to commit") {
			return nil
		}
		return fmt.Errorf("git commit: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// copyTreeLenient is copyTree for a source nobody controls (another tool's live
// session directory): an entry that cannot be copied — a dangling symlink, a
// symlink to a directory, a file that vanished or is unreadable — is skipped
// and reported, one string per entry, instead of failing the whole copy. The
// strict copyTree stays for sources a caller owns, where a failure is a bug.
// maxLenientFileSize bounds one file of a foreign tree (a stray core dump or
// build artefact in a scratchpad must not bloat the git archive).
const maxLenientFileSize = 256 << 20

func copyTreeLenient(src, dst string) (skipped []string, err error) {
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, werr error) error {
		rel, _ := filepath.Rel(src, path)
		if werr != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", rel, werr))
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			if d.Name() == ".git" && rel != "." {
				// A nested repository would be staged as an empty gitlink,
				// losing its content while looking archived.
				skipped = append(skipped, rel+": nested git directory not copied")
				return fs.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		}
		info, serr := os.Stat(path)
		if serr != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", rel, serr))
			return nil
		}
		if !info.Mode().IsRegular() {
			skipped = append(skipped, fmt.Sprintf("%s: not a regular file (%s)", rel, info.Mode().Type()))
			return nil
		}
		if info.Size() > maxLenientFileSize {
			skipped = append(skipped, fmt.Sprintf("%s: %d bytes exceeds the %d byte limit", rel, info.Size(), int64(maxLenientFileSize)))
			return nil
		}
		if cerr := copyFile(path, target); cerr != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", rel, cerr))
		}
		return nil
	})
	return skipped, err
}
