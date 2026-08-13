package filemod

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnreadableTree is returned when a stat fails for a reason other than the
// path being absent — a permission denied on a parent, an I/O error, a name
// the filesystem will not take.
//
// It is deliberately not folded into "absent". A stat that fails this way is
// not a fact about the tree, it is the machine being unable to answer, and the
// two lead to opposite events: taken as absent, a file sitting right there
// behind an unreadable parent is announced as PostFileDelete, and every rule
// bound to deletion is asked about a file the project still has. Refusing to
// answer is the honest result, and the engine prints it.
var ErrUnreadableTree = errors.New("filemod: cannot determine whether the path is on disk")

// ErrPathNotRelativeToRoot is returned for a path that does not name a file
// inside the repository: absolute, escaping through "..", naming the root
// itself, or reaching outside through a symlinked parent directory.
//
// Rejected rather than normalised. filepath.Join(root, "../outside.md") folds
// the escape away and stats a real file outside the repository, but the event
// still carries the raw "../outside.md" — unresolvable by any hook, and an
// escape again the moment a hook joins it against its own root. The events
// carry repository-relative paths by contract; a path that cannot be one is
// not a path this module can report.
//
// The symlinked-parent case is the same escape reached without any lexical
// tell, and it is the worse of the two: "escape/id_rsa", where "escape" is a
// link to a directory outside the repository, survives every string check and
// the path it emits JOINS cleanly, so a hook reads the outside file through it
// and nothing anywhere reports a problem. A containment check that never
// touches the filesystem cannot see this, so resolve does one.
var ErrPathNotRelativeToRoot = errors.New("filemod: path is not relative to the repository root")

// ErrNoRoot is returned when an Observed names paths without naming what they
// are relative to. Joining against "" resolves them against the process's
// working directory, which is the dependence on the invocation site that
// Root() exists to remove.
var ErrNoRoot = errors.New("filemod: paths given with no repository root")

// ErrPathIsNotAFile is returned for a path where a directory now sits. The
// kinds this module declares are about files; a create or update naming a
// directory hands every file rule something it was not written against.
var ErrPathIsNotAFile = errors.New("filemod: path is not a regular file")

// ErrNotADifference is returned for a path that was not at the baseline and is
// not on disk now — the no/no row.
//
// Observed's contract is that its paths DIFFER from the baseline, so such a
// path differs from nothing and the producer named it in error. It is
// indistinguishable on the tree from a file created and removed inside one
// cycle, which is legitimate and yields no event; reporting both keeps the
// legitimate case silent in the event stream while still making a producer that
// is always answering false — deletes vanish, updates become creates — say so
// somewhere instead of degrading quietly. No producer exists yet, so this is
// the only check the first one will meet.
var ErrNotADifference = errors.New("filemod: path differs from neither the baseline nor the tree")

// presence is what a stat can tell us about a path.
//
// Four states rather than three, because two independent questions were each
// answered with a tri-state and both answers are needed. One axis is whether
// the lookup produced a FACT at all — a real ENOENT is a fact about the tree,
// and any other failure is the machine declining to answer. The other is what
// KIND of object is there when something is: the kinds this module declares are
// about regular files, and a directory is neither a file that changed nor a
// path that is free.
//
// Collapsing either axis loses a defect the other cannot catch. Folding
// unknown into absent announces PostFileDelete for a file sitting behind an
// unreadable parent. Folding notAFile into present emits PreFileUpdate for a
// write aimed at a directory — an event claiming a file is about to be modified
// when no file is there and the write cannot land.
type presence int

const (
	// absent: the stat said the path is not there, which is a fact.
	absent presence = iota
	// presentFile: an object is there and it is one this module's kinds are
	// about. A symlink counts — see lookAt on why the link, not its target.
	presentFile
	// presentNotAFile: something is there that a file write cannot land on —
	// a directory, a device, a socket. It exists, so it is not absent; it is
	// not a file, so no file event can honestly be about it.
	presentNotAFile
	// unknown: the stat failed for some other reason. Not a fact about the
	// tree — the machine could not answer.
	unknown
)

// lookAt reports what is at a path.
//
// os.Lstat rather than os.Stat, because Stat resolves symlinks and this module
// is asked about the path git tracks, not about wherever it points. A symlink
// to a not-yet-generated target reads as absent under Stat, so an agent
// creating one gets no event at all and no guardrail ever sees the new file;
// retargeting an existing link to a missing path reads as a delete of a file
// that is still sitting there. Lstat sees the link itself, which is the thing
// that changed.
//
// A path where a non-file sits is reported presentNotAFile along with
// ErrPathIsNotAFile. The value and the error carry the same fact on purpose,
// because the two callers need it in different forms: the observed phase treats
// it as a producer error and reports it, while the pre phase is not looking at a
// producer at all and simply has no event to emit. A bool return could not serve
// both — that is exactly how a write onto a directory became PreFileUpdate, the
// distinction being available only as an error the pre path had no use for and
// therefore discarded.
//
// isRegular rather than !IsDir. A device or a socket is no more writable-as-a-
// file than a directory is, and a check that names only directories leaves the
// rest reported as files.
//
// It is stat'd by its absolute path and reported by its repository-relative
// one. Every other error this module raises names the relative path, and these
// are the diagnostics someone debugging a producer reads — a lone absolute path
// among them, in a module whose whole premise is that paths are relative to a
// root, reads as a different kind of thing than it is. The underlying
// *PathError still carries the absolute path for anyone who needs it.
func lookAt(path, full string) (presence, error) {
	info, err := os.Lstat(full)
	switch {
	case err == nil:
		if !isRegular(info) {
			return presentNotAFile, fmt.Errorf("%w: %s", ErrPathIsNotAFile, path)
		}
		return presentFile, nil
	case errors.Is(err, os.ErrNotExist):
		return absent, nil
	default:
		return unknown, fmt.Errorf("%w: %s: %w", ErrUnreadableTree, path, err)
	}
}

// isRegular reports whether the object AT a path is one this module's kinds are
// about, judging the link itself and never what it points at.
//
// A symlink is a regular file for this purpose, and that is the whole reason
// this is not a bare Mode().IsRegular(). What git records for a symlink settles
// it: a blob holding the target's PATH. The link is a file in the repository
// whatever sits at the other end, so a PostFileCreate naming one is accurate,
// and a rule reading it reads the link.
//
// The alternative was tried and rejected. Following the link with os.Stat does
// make ErrPathIsNotAFile fire for a link-to-directory — but it decides whether a
// tracked file gets an event by looking at something the repository does not
// contain, and it re-couples this module to Stat in exactly the place lookAt's
// doc rejects it. A link to an outside directory would then produce no event,
// though git tracks it and changing it is a change to the repository; and the
// same link would flip between "file" and "not a file" as its target is created,
// removed, or retargeted, so a real change to a tracked file goes silent on the
// strength of a directory somewhere else. That is the failure mode this module
// exists to prevent, and it is worse than the one being fixed.
//
// The guard's purpose — keeping rules written about files from being handed a
// directory — is met for every directory the repository actually holds, which
// is the set of directories a path check can honestly speak about.
//
// The cost is stated rather than hidden: a rule that resolves the path it is
// given and walks it will find a directory behind such a link. That is a
// property of following symlinks, which the rule chose to do, and no answer here
// prevents it.
func isRegular(info os.FileInfo) bool {
	return info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0
}

// resolve checks that a repository-relative path names a file inside the
// repository, and returns it in its one canonical spelling along with the
// absolute path to stat.
//
// The canonical spelling matters as much as the check: "a.md" and "./a.md" are
// one file, and left as given they produce two events with different `path`
// values — a matcher scoped to `a.md` catches one and misses the other, and a
// fingerprint keyed on path records two independent verdicts for one file.
//
// The lexical checks come first and the filesystem one last, because the
// lexical ones are the cheap way to reject the spellings that are wrong on
// their face and they need no syscall to be right. What they cannot see is a
// parent directory that is a symlink out of the repository, which is why
// contained follows them rather than replacing them.
func resolve(root, path string) (clean, full string, err error) {
	if strings.TrimSpace(root) == "" {
		return "", "", fmt.Errorf("%w: %q", ErrNoRoot, path)
	}
	if path == "" || strings.TrimSpace(path) == "" {
		// Names no file. Joined against the root it would stat the root, which
		// exists, and turn into an update of nothing.
		return "", "", fmt.Errorf("%w: %q names no file", ErrPathNotRelativeToRoot, path)
	}
	if filepath.IsAbs(path) {
		return "", "", fmt.Errorf("%w: %q is absolute", ErrPathNotRelativeToRoot, path)
	}

	clean = filepath.Clean(filepath.FromSlash(path))
	if clean == "." || clean == string(filepath.Separator) {
		// "." and "./" reach the same root stat the empty path would.
		return "", "", fmt.Errorf("%w: %q names the root, not a file within it", ErrPathNotRelativeToRoot, path)
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("%w: %q escapes it", ErrPathNotRelativeToRoot, path)
	}

	full = filepath.Join(root, clean)
	if err := contained(root, full); err != nil {
		return "", "", fmt.Errorf("%w: %q: %w", ErrPathNotRelativeToRoot, path, err)
	}
	return clean, full, nil
}

// contained reports whether full, once every symlink above its last element is
// followed, still lies under root.
//
// It asks about the PARENTS and deliberately not about the leaf. Which object a
// leaf symlink points at is lookAt's question and lookAt answers it with Lstat
// on purpose — the link is the thing git tracks, and a link to somewhere outside
// the repository is still a file inside it that changed. Where the link's own
// DIRECTORY sits is a different question: if that is outside, the repository
// does not contain the named file at all, under any reading.
//
// The walk goes up from the parent to the first ancestor that exists, because
// the path may legitimately not be on disk — a delete names a file that is gone,
// and a whole removed directory takes its parents with it. Resolving only what
// exists is what lets those keep working; the ancestors that do not exist cannot
// be symlinks, so there is nothing about them left to check.
//
// The root is resolved too. A repository reached through a symlinked ancestor is
// ordinary — /tmp is a link to /private/tmp on darwin, which every test here
// runs under — and comparing a resolved child against an unresolved root would
// call every path in such a repository an escape.
func contained(root, full string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		// The root itself cannot be resolved. Not something a path can be
		// blamed for, and not something to pass silently either: no containment
		// claim can be made at all, so none is.
		return fmt.Errorf("cannot resolve the repository root %s: %w", root, err)
	}

	// Walk up to the first ancestor that is on disk. dir is always a prefix of
	// full's parent, so this terminates at realRoot's own ancestors at worst.
	dir := filepath.Dir(full)
	unresolved := ""
	for {
		real, err := filepath.EvalSymlinks(dir)
		if err == nil {
			return under(realRoot, filepath.Join(real, unresolved))
		}
		if !errors.Is(err, os.ErrNotExist) {
			// Permission denied on a parent, say. Presence is lookAt's to
			// report as ErrUnreadableTree; containment simply cannot be
			// established, and letting the path through on a failed check is
			// the one outcome this function exists to prevent.
			return fmt.Errorf("cannot resolve %s: %w", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the filesystem root without finding anything that exists.
			return fmt.Errorf("no part of %s is on disk", full)
		}
		unresolved = filepath.Join(filepath.Base(dir), unresolved)
		dir = parent
	}
}

// under reports whether path is realRoot or sits beneath it, comparing whole
// path elements. A prefix test on the raw string would accept "/repo-backup"
// for a root of "/repo".
func under(realRoot, path string) error {
	rel, err := filepath.Rel(realRoot, path)
	if err != nil {
		return fmt.Errorf("%s is not reachable from %s", path, realRoot)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s resolves outside the repository", path)
	}
	return nil
}
