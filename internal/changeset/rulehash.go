package changeset

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sloprail/sloprail/internal/gitrepo"
)

// RuleHash hashes every file under a rule's folder dir: the rule's own yaml, scripts and
// templates. Editing any of them changes the hash, and a verdict keyed on the old one no
// longer applies. a10n's key left the rubric out, and kept serving passes reached under a
// rubric that no longer existed.
//
// RuleHash hashes everything on disk under dir (RuleHashAt narrows that to what the engine
// executes for a guard in a repository). Each file
// contributes its path, whether it is executable (a script losing its bit
// changes what runs), and its bytes; a symlink contributes its target rather
// than what it points at, so a link swung elsewhere is a change even when both
// ends read the same. Anything that cannot be read is an error: a hash over a
// folder that was only partly read would be a hash of a different rule.
//
// .DS_Store is the one thing left out — a file the operating system writes into
// folders a person merely looked at, which would otherwise reset a rule's
// verdicts for no change of the rule.
func RuleHash(dir string) (string, error) {
	return ruleHashOnDisk(dir)
}

// RuleHashAt is the hash of the bytes the engine EXECUTES for the guard whose folder is dir:
// the files of the folder as they are on disk, restricted to
//
//   - the files git TRACKS in the repository at repo, so an uncommitted edit of a tracked
//     rule file (script, template, prompt, file-guard.yaml) changes the hash and its verdict
//     never matches the committed rule's, while an untracked or ignored file a check writes
//     into its own folder (a ledger, a cache) does not; and
//   - for a rule with nothing tracked yet (new, uncommitted), every file git does not ignore.
//
// A plugin's rule (plugin true) lives outside the repository and has no git: all of its
// on-disk files are hashed (an installed plugin's folder is never written to). `run` and
// `verify` call this with the same arguments, so one rule has one hash in both.
//
// It fails closed: a git error, or a dir outside the repository for a rule that is not a
// plugin's, is an error, never a fallback to another hash. Both paths are symlink-resolved
// before they are compared (macOS /tmp is /private/tmp).
func RuleHashAt(repo, dir string, plugin bool) (string, error) {
	if plugin {
		return RuleHash(dir)
	}
	realRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		return "", fmt.Errorf("changeset: hash rule %s: %w", dir, err)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("changeset: hash rule %s: %w", dir, err)
	}
	rel, err := filepath.Rel(realRepo, realDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("changeset: hash rule %s: it is outside the repository %s", dir, repo)
	}
	rel = filepath.ToSlash(rel)
	names, err := gitrepo.TrackedFiles(realRepo, rel)
	if err != nil {
		return "", fmt.Errorf("changeset: hash rule %s: %w", dir, err)
	}
	if len(names) == 0 {
		if names, err = gitrepo.UnignoredFiles(realRepo, rel); err != nil {
			return "", fmt.Errorf("changeset: hash rule %s: %w", dir, err)
		}
	}
	set := map[string]bool{}
	for _, n := range names {
		set[strings.TrimPrefix(strings.TrimPrefix(n, rel), "/")] = true
	}
	return hashFolder(realDir, func(r string) bool { return set[filepath.ToSlash(r)] })
}

func ruleHashOnDisk(dir string) (string, error) { return hashFolder(dir, nil) }

// hashFolder hashes dir's entries; with keep non-nil only the files and links it accepts
// (relative, slash-separated); a kept name gone from disk is simply absent from the hash.
func hashFolder(dir string, keep func(rel string) bool) (string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("changeset: hash rule %s: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("changeset: rule %s is not a folder", dir)
	}
	h := sha256.New()
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dir || d.Name() == ".DS_Store" {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if keep != nil && (info.IsDir() || !keep(rel)) {
			return nil
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			writePart(h, "link", filepath.ToSlash(rel), target)
		case info.IsDir():
			writePart(h, "dir", filepath.ToSlash(rel))
		case info.Mode().IsRegular():
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			mode := "file"
			if info.Mode().Perm()&0o111 != 0 {
				mode = "exec"
			}
			writePart(h, mode, filepath.ToSlash(rel), string(body))
		default:
			return fmt.Errorf("changeset: %s is neither a file, a folder nor a link", path)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("changeset: hash rule %s: %w", dir, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// writePart writes length-prefixed strings, so no two folders can hash the same
// by having their names and contents cut at different points.
func writePart(h interface{ Write([]byte) (int, error) }, parts ...string) {
	for _, p := range parts {
		fmt.Fprintf(h, "%d:", len(p))
		h.Write([]byte(p))
	}
}
