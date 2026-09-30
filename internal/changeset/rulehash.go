package changeset

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// RuleHash hashes a rule's whole `.sloprail` ROOT (the project's or its plugin's,
// the caller passes it: FileGuard.Root): the rule's own yaml, scripts and
// templates, and everything else under it — the other rules, schemas, shared
// scripts. What a rule does depends on all of it, and a hash over the rule's
// folder alone kept serving passes reached under a schema that had since changed.
//
// Editing any of them changes the hash, and a verdict or a watermark keyed on
// the old one no longer applies. a10n's key left the rubric out, and kept
// serving passes reached under a rubric that no longer existed.
//
// What is hashed is what is on disk, because that is what will run. Each file
// contributes its path, whether it is executable (a script losing its bit
// changes what runs), and its bytes; a symlink contributes its target rather
// than what it points at, so a link swung elsewhere is a change even when both
// ends read the same. Anything that cannot be read is an error: a hash over a
// folder that was only partly read would be a hash of a different rule.
//
// .DS_Store is the one thing left out — a file the operating system writes into
// folders a person merely looked at, which would otherwise reset a rule's
// watermark for no change of the rule.
func RuleHash(dir string) (string, error) {
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
