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

// ErrBaselineKeyedOnRawSpelling is returned when a producer's baseline answers
// differently for a path's raw spelling than for its canonical one.
//
// Observed.ExistedAtBaseline is asked with the canonical spelling and says a
// producer keying a map on its own paths "must clean them first". Left at that,
// the contract was honor-system in the one place a violation is invisible: a
// producer whose map holds "./dir/./a.md" is asked about "dir/a.md", answers
// false for a file that WAS at the baseline, and the update goes out as a
// create. Every delete vanishes the same way. ErrNotADifference does not catch
// it — these paths are on disk, so the no/no row is never reached — which left
// the module's one guard against silent baseline degradation not covering the
// failure mode the canonical-spelling rule itself introduced.
//
// So where a path's raw spelling differs from its canonical one, the baseline is
// asked BOTH ways. Agreement is the ordinary answer and costs one extra call on
// non-canonical spellings only. Disagreement is not a judgement call about the
// producer's internals: it is the producer answering two ways about one file,
// which its own contract says cannot happen. The path is reported rather than
// classified, because which of the two answers is the true one is exactly what
// is now in doubt.
//
// It cannot catch a producer that cleans its keys and gets the baseline wrong
// anyway — nothing can, short of the git history the producer alone holds. What
// it catches is the mechanical version, which is the one a first producer will
// actually ship.
var ErrBaselineKeyedOnRawSpelling = errors.New("filemod: baseline answers differently for the raw and canonical spellings of one path")

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
//
// One consequence of cleaning first is worth naming, because it is the same
// Clean-versus-symlink mismatch the containment check exists for. Clean folds
// ".." lexically, before any filesystem sees the path: "sub/escape/../id_rsa",
// where "escape" is a link elsewhere, becomes "sub/id_rsa", while a kernel
// open() follows "escape" and then steps up from wherever it LANDED. The two
// name different files. What this module emits is the folded one, and every
// check it then runs — containment included — is about that path rather than
// about the one an open() would reach.
//
// It is not a disclosure: the folded path is what the event carries and what a
// hook joins, so consumers all read the same file this module classified, and
// that file is inside the repository or the path was refused. The cost is
// narrower and real — a cycle that touched the file the kernel would reach gets
// an event naming a different one, so a rule about the touched file does not
// fire. Fixing it means resolving the path element by element instead of
// cleaning it, which is the TOCTOU-bound walk contained already is; the honest
// statement is that paths are classified as CLEANED, and a producer emitting
// ".." through a symlinked directory is naming something it did not mean to.
// git does not produce such paths, and the contract already asks for clean ones.
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
// The walk goes up from the parent to the first ancestor that RESOLVES, because
// the path may legitimately not be on disk — a delete names a file that is gone,
// and a whole removed directory takes its parents with it. Resolving only what
// is there is what lets those keep working.
//
// Skipping an ancestor requires proving there is nothing to follow, and the
// proof is an Lstat, not the ENOENT that EvalSymlinks returned. The two are not
// the same fact: EvalSymlinks reports ENOENT for a DANGLING symlink as readily
// as for a name with nothing at it, and a dangling symlink is an ancestor that
// does not exist and is a symlink. Read as "not on disk, keep going up", it is
// skipped and the containment answer is taken from an ancestor ABOVE it — the
// check walking straight past the one object it exists to look at, and settling
// containment from the root it never left. Its target is absent only until the agent that
// made the link makes the directory, which is usually moments later, and the
// path this emits joins cleanly onto the root and reads whatever lands there.
// So each skipped ancestor is Lstat'd, and anything that answers is refused:
// where an unresolvable link will point is not knowable now, and containment
// that cannot be established is not containment.
//
// The root is resolved too. A repository reached through a symlinked ancestor is
// ordinary — /tmp is a link to /private/tmp on darwin, which every test here
// runs under — and comparing a resolved child against an unresolved root would
// call every path in such a repository an escape.
//
// What this does NOT establish, and no check of this shape can: that the tree
// still looks this way when a consumer reads the path. Every answer here is
// about the tree at the instant of the walk, and a link swung between this call
// and the hook that acts on the event resolves somewhere else. What is
// guaranteed is narrower and still worth having — no path is emitted whose
// parents resolved outside the root, or whose containment could not be
// established at all, at classification time.
//
// Hard links are contained on purpose. repo/hardlink pointing at an outside
// inode resolves under the root because it IS an entry under the root — a name
// the repository holds and git tracks, with no link to follow. Refusing it would
// mean refusing an ordinary file for a property no path check can see, and the
// silence that produces is the worse failure. The boundary this function draws
// is over PATHS, not over inodes.
//
// The ancestors the walk skips are NOT carried along and re-joined onto the one
// that resolved, though an earlier version did that. It could not affect any
// answer: full arrives already cleaned, so every skipped element is an ordinary
// name and the rejoined path only ever DESCENDS from the resolved ancestor.
// Descending cannot leave a directory that is contained, nor re-enter one that
// is not, so the verdict is settled by the resolved ancestor alone and the
// remainder was decoration on it. Carrying it looked like extra rigour while
// giving the check nothing, and left two ways to get it subtly wrong — dropped,
// or assembled in reverse — that no test could distinguish because no behaviour
// depended on either. What the walk owes is that it resolved everything it
// passed, which is now the Lstat's job and not the remainder's.
func contained(root, full string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		// The root itself cannot be resolved. Not something a path can be
		// blamed for, and not something to pass silently either: no containment
		// claim can be made at all, so none is. Refusing here is the same rule
		// the ancestors get — an unestablished containment is refused, and the
		// root is not exempt from it for being the root.
		return fmt.Errorf("cannot resolve the repository root: %w", err)
	}

	// Walk up to the first ancestor that resolves. dir is always a prefix of
	// full's parent, so this terminates at realRoot's own ancestors at worst.
	dir := filepath.Dir(full)
	for {
		real, err := filepath.EvalSymlinks(dir)
		if err == nil {
			return under(realRoot, real)
		}
		if !errors.Is(err, os.ErrNotExist) {
			// Permission denied on a parent, say. Presence is lookAt's to
			// report as ErrUnreadableTree; containment simply cannot be
			// established, and letting the path through on a failed check is
			// the one outcome this function exists to prevent.
			return fmt.Errorf("cannot resolve an ancestor: %w", err)
		}
		// EvalSymlinks said ENOENT. That is not yet permission to skip dir:
		// ask the filesystem about dir ITSELF, without following anything.
		switch info, lerr := os.Lstat(dir); {
		case lerr == nil && info.Mode()&os.ModeSymlink != 0:
			// A dangling symlink. It is right here, it is a link, and where it
			// leads is unknowable until its target exists — which says nothing
			// about where it will lead then.
			//
			// EvalSymlinks's error is deliberately NOT wrapped here, alone among
			// these branches. Its *PathError names the path that did not
			// resolve, which for a dangling link is the link's TARGET — the one
			// string in this whole function that is both outside the repository
			// and chosen by whoever made the link. Wrapping it would put the
			// escape's destination in the log as the reward for refusing it. The
			// ancestor's own name is what a producer needs and it is inside the
			// repository, so that is what is said.
			return fmt.Errorf("an ancestor is a symlink that does not resolve: %s", filepath.Base(dir))
		case lerr == nil:
			// dir exists and is not a link, yet EvalSymlinks could not resolve
			// it: something further along its own path did not answer.
			// Unestablished, so refused.
			return fmt.Errorf("cannot resolve an ancestor: %w", err)
		case !errors.Is(lerr, os.ErrNotExist):
			return fmt.Errorf("cannot resolve an ancestor: %w", lerr)
		}
		// Only now is dir known to hold nothing at all, so there is nothing
		// about it to follow and the walk may go up.
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the filesystem root without finding anything that exists.
			return errors.New("no part of the path is on disk")
		}
		dir = parent
	}
}

// under reports whether path is realRoot or sits beneath it, comparing whole
// path elements. A prefix test on the raw string would accept "/repo-backup"
// for a root of "/repo".
//
// The ".." test is anchored to the separator for the same reason the lexical one
// in resolve is, and it is a separate copy of the anchor that needs its own
// proof: a real directory named "..hidden" makes Rel return "..hidden/a.md", and
// a bare HasPrefix(rel, "..") calls that an escape. A file genuinely changing in
// the tree would then produce no event at all — the silence, reached from the
// direction that looks like caution.
//
// Comparison is case-sensitive, which on a case-insensitive filesystem (darwin's
// default) is a known false negative rather than an oversight: a root spelled
// /parent/REPO and a resolved path under /parent/repo name one directory the
// kernel cannot tell apart, and this calls the second an escape. It errs toward
// refusing, so nothing outside the repository is let through by it; what it
// costs is an event for a real change, which is the worse direction, and folding
// case would cost the opposite on a case-SENSITIVE filesystem where "repo" and
// "REPO" are two directories. Neither is free, and the one that never emits a
// path it should not is the one taken.
//
// The Rel error is refused rather than reported as an escape, and it is worth
// saying that no input reaching here through contained can produce it: Rel fails
// only on a mixed absolute/relative pair, and both arguments come out of
// EvalSymlinks, which preserves the absoluteness of what it was given — so the
// two agree whatever the caller's root was. A mutation replacing this branch
// with anything at all therefore survives every test, and no test can honestly
// kill it. It is kept because "unreachable" is a claim about today's callers
// rather than about the function, and the alternative to refusing is treating an
// unanswerable comparison as containment.
func under(realRoot, path string) error {
	rel, err := filepath.Rel(realRoot, path)
	if err != nil {
		return errors.New("the path is not reachable from the repository root")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("the path resolves outside the repository")
	}
	return nil
}
