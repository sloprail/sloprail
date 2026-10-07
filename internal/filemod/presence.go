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
// are relative to, or names them against a root that is not absolute.
//
// Joining against "" resolves them against the process's working directory,
// which is the dependence on the invocation site that Root() exists to remove.
// Every other relative spelling does the same thing and the guard was written
// for "" alone: Root() == "." is the natural way to say "here", it survived the
// emptiness check, and filepath.Join(".", "secrets/id_rsa") is a RELATIVE path
// the kernel resolves against whatever directory the process happens to be in.
// Containment did not catch it either — EvalSymlinks(".") resolves to that same
// directory, so the cwd IS the root by the time under() compares them, and a
// file outside the repository is contained under it correctly and uselessly.
//
// So the requirement is absoluteness, not non-emptiness. It is the property
// that actually makes a root independent of the invocation site, and "" is
// merely one spelling that lacks it.
var ErrNoRoot = errors.New("filemod: paths given with no absolute repository root")

// ErrPathIsNotAFile is returned for a path where a directory now sits. The
// kinds this module declares are about files; a create or update naming a
// directory hands every file rule something it was not written against.
//
// A symlink TO a directory is not one of these, on purpose — see isDirectory.
// git tracks the link as a file, and deciding its kind by what it points at
// would make a tracked file's event depend on an object outside the repository.
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

// maxSymlinkHops bounds how far a chain of dangling symlinks is followed before
// it is called unsettleable and refused. The kernel's own limit is this order of
// magnitude; what matters here is that the walk terminates on a chain that does
// not settle — which no single Lstat can detect — and refuses rather than allows
// when it gives up.
//
// The bound is observable behaviour on a static tree, not defence in depth
// against something unreachable — which an earlier comment on the cycle test
// claimed, and this contradicts on purpose. A CYCLE is caught before the walk
// starts: EvalSymlinks fails ELOOP, which is not ENOENT, so contained refuses at
// the branch above. A DANGLING chain is not — its end does not exist, so
// EvalSymlinks answers ENOENT, the dangling-link branch is taken, and this loop
// follows it hop by hop with nothing else to stop it. Forty-one dangling links
// reach the bound on a static tree, with no cycle and no race.
//
// Both directions of being wrong have a cost, and refusing too eagerly is the
// SILENCE direction: a generated-output chain entirely inside the repository —
// the case danglingLinkStaysInside exists to let through — produces no event at
// all when it is refused. Refusing too late risks a walk that does not terminate.
// TestResolve_TheHopBoundIsWhereTheChainStopsBeingFollowed pins both sides.
//
// What this number is NOT is the thing standing between a real chain and a wrong
// answer, and saying so keeps the paragraph above from being read as more than it
// is. The kernel gives up long before forty: darwin's MAXSYMLINKS is 32 and every
// element of a path spends from the same budget, so an Lstat through a chain of
// this length fails ELOOP under twenty links. Chains between the kernel's limit
// and this one are contained here and then unreadable at lookAt — which reports
// ErrUnreadableTree, the honest "could not answer", rather than absence. So the
// bound's live job is termination on a chain the kernel has not already rejected,
// and its exact position is defensible rather than load-bearing.
// TestResolve_TheHopBoundSitsAboveTheKernelsOwn records that relationship so the
// two limits are not confused for one.
const maxSymlinkHops = 40

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
// It is stat'd by `full` and reported by `path`, so a caller that HAS both
// spellings reports the repository-relative one: a lone absolute path among
// these diagnostics, in a module whose whole premise is that paths are relative
// to a root, reads as a different kind of thing than it is. The underlying
// *PathError still carries the absolute path for anyone who needs it.
//
// Only extractObserved has both and passes them separately. The command and
// pending callers pass one spelling twice, so on those paths the reported
// string is whatever the producer wrote — which may be absolute. That is a
// diagnostic difference and nothing more: these errors reach stderr, never a
// matcher, and the EVENT's path is canonicalised by Reportable() regardless.
// Said outright because this comment previously claimed every error names the
// relative path, which was true of one caller out of four.
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

// isDirectory reports whether a path holds a directory right now.
//
// A SECOND question, not a second oracle, and the distinction is what keeps
// TestLookAt_IsTheOnlyPresenceOracle satisfied by it living here rather than
// being written out at its caller. lookAt answers "can a file event honestly be
// about this path", and its answer folds a directory, a device and a socket
// into one value — presentNotAFile — because none of the three can receive a
// file write, which is all that question needs.
//
// A copy needs the difference. `cp a.md target/` writes target/a.md when target
// is a directory and does something else entirely when target is a socket, so
// the caller in extract.go must separate exactly the two cases lookAt is right
// to merge. Asking here, next to the oracle, is what stops that from becoming
// the second independent stat the enumeration test exists to forbid — and it
// keeps the Lstat argument below in ONE place, so a change to it cannot leave
// two callers reading the tree by different rules.
//
// Lstat rather than Stat, for the same reason lookAt uses it and one more: a
// symlink TO a directory is not reported as one. `cp a.md link-to-dir` does
// follow the link on a real system, but predicting where it lands means
// resolving a path the command line did not name — a guess about the tree
// rather than a reading of it, which is what this module refuses everywhere
// else. The cost is an unclaimed result on a spelling nobody writes.
//
// An unreadable path is not a directory. The caller's fallback is to treat the
// operand as an ordinary destination, which is the honest answer when the tree
// could not be read: it claims a path rather than inventing several inside one.
func isDirectory(full string) bool {
	info, err := os.Lstat(full)
	return err == nil && info.IsDir()
}

// readSize is how many bytes contentOnDisk would read at a path: the size of a
// regular file, following a link as the read does. It answers a BUDGET question
// (expandRemovedDirectories bounds what one removal reads), never a presence
// one — anything that is not a regular file, or cannot be stat'ed, is 0 here
// and is classified by lookAt like any other target. It lives in this file
// because this is the one file that stats.
func readSize(full string) int64 {
	info, err := os.Stat(full)
	if err != nil || !info.Mode().IsRegular() {
		return 0
	}
	return info.Size()
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
// sr:invariant events/path-inside-or-absolute
func resolve(root, path string) (clean, full string, err error) {
	// One check rather than two. An emptiness test used to stand here as well,
	// and absoluteness subsumes it exactly — "" and "   " are not absolute — so
	// keeping both left a branch no input could reach past the other and no test
	// could tell was gone. What the emptiness test was reaching for is this: a
	// root that does not start at the filesystem root is joined into a RELATIVE
	// full path, which the kernel resolves against the process's working
	// directory. That is the dependence on the invocation site Root() exists to
	// remove, and "" was only ever one spelling of it.
	// Judged as given, not trimmed. Trimming here would accept "  /repo" and then
	// JOIN the untrimmed spelling, so the string that was checked and the string
	// that gets stat'd would differ — and a root carrying stray whitespace is a
	// producer bug either way, which this says instead of quietly papering over.
	if !filepath.IsAbs(root) {
		return "", "", fmt.Errorf("%w: %q is not an absolute path", ErrNoRoot, root)
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
// So each skipped ancestor is Lstat'd, and only a name with nothing at it is
// skipped.
//
// A dangling link that answers there is then judged rather than refused
// outright. Blanket refusal was the wider mistake in the safe-looking
// direction: repo/pending -> repo/generated, a link made before the directory
// it names, is the ordinary generated-output case with both ends inside the
// repository, and refusing it produced no event at all — the silence, reached
// from the direction that looks like caution. "Where an unresolvable link will
// point is not knowable now" was simply false: os.Readlink says where it points
// whether or not the target exists. So the target is read and judged, and only
// one that cannot be placed inside the root is refused. See
// danglingLinkStaysInside.
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
// answer, and the reason is the Lstat above rather than anything about the
// path's spelling: an ancestor is skipped only when EvalSymlinks AND Lstat both
// say ENOENT, and an Lstat returning ENOENT means there is NOTHING at that name
// — no link, no directory, no file. A name with nothing at it cannot redirect
// anything, so no skipped element can move the verdict off the ancestor that
// resolved, and the remainder was decoration on it.
//
// That the path arrives Cleaned, so the rejoin only ever descends, is true and
// beside the point. It is a property of today's callers; the Lstat is a property
// of this loop, and it is what would still hold if a caller someday passed
// something less tidy. Stating the weaker reason would leave this paragraph
// quietly false the moment the Lstat guard changed.
//
// Carrying the remainder looked like extra rigour while giving the check
// nothing, and left two ways to get it subtly wrong — dropped, or assembled in
// reverse — that no test could distinguish because no behaviour depended on
// either. What the walk owes is that it resolved everything it passed, which is
// the Lstat's job and not the remainder's.
func contained(root, full string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		// The root itself cannot be resolved. Not something a path can be
		// blamed for, and not something to pass silently either: no containment
		// claim can be made at all, so none is. Refusing here is the same rule
		// the ancestors get — an unestablished containment is refused, and the
		// root is not exempt from it for being the root.
		//
		// The *PathError is not wrapped, for the reason every message here says
		// only what it must: it names the root's absolute path, and the module's
		// own sentences are repository-relative throughout. There is nothing
		// relative to say about the root, so nothing is said about it.
		return errors.New("cannot resolve the repository root")
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
			//
			// Written inline rather than through ancestorUnresolvable, and the
			// two are EQUIVALENT: that helper's body is this same Errorf with
			// this same format and this same argument, and it discards the error
			// it is handed. Rewriting this line as a call to it changes no
			// message, no verdict and no test. Recorded here so the swap is
			// understood as an equivalence rather than mistaken for this branch
			// being untested the next time it survives a mutation.
			//
			// Both spellings exist because the helper is named for the two
			// branches BELOW, which no static tree reaches and which therefore
			// need something a test can call directly. This branch is reachable
			// and is pinned through resolve by
			// TestContained_AnEvalSymlinksFailingForItsOwnReasonIsRefused.
			return fmt.Errorf("cannot resolve an ancestor: %s", relativeTo(realRoot, dir))
		}
		// EvalSymlinks said ENOENT. That is not yet permission to skip dir:
		// ask the filesystem about dir ITSELF, without following anything.
		switch info, lerr := os.Lstat(dir); {
		case lerr == nil && info.Mode()&os.ModeSymlink != 0:
			// A dangling symlink. It is right here, it is a link, and
			// EvalSymlinks cannot say where it leads because its target is not
			// on disk yet.
			//
			// Refusing every one of these was wider than the threat. os.Readlink
			// answers exactly the question the refusal called unanswerable: the
			// target is written in the link, target or no target. A link whose
			// target is inside the repository is the ordinary generated-output
			// case — repo/pending -> repo/generated, made before the directory it
			// names, both ends inside — and a blanket refusal turns that into no
			// event at all, which is the silence this module exists to prevent.
			//
			// So the target is read and judged, and only what cannot be placed
			// inside the root is refused. A relative target is resolved against
			// the link's own directory, the way the kernel would. The judgement
			// is lexical on purpose: the target does not exist, so there is
			// nothing to resolve it against, and a lexical answer that can only
			// be wrong by refusing is the safe half of the trade. What it costs
			// is a link pointing at an inside path that is ITSELF reached through
			// a symlink out — refused for want of proof, which is the direction
			// this function refuses in everywhere else.
			return danglingLinkStaysInside(realRoot, dir)
		case lerr == nil:
			// dir exists and is not a link, yet EvalSymlinks could not resolve
			// it: something further along its own path did not answer.
			// Unestablished, so refused.
			//
			// Unreachable from any static tree, for the same structural reason as
			// the branch below — see ancestorUnresolvable. EvalSymlinks returns
			// ENOENT only when its own Lstat of dir or a prefix of dir said so,
			// and in either case an Lstat of dir here says ENOENT too and the
			// skip is taken. Only a race separates them. Kept because the
			// direction has to be right whichever way the tree moves under it,
			// and pinned by TestContained_ANonSymlinkAncestorThatWillNotResolveIsRefused
			// calling the decision rather than staging a tree that cannot exist.
			return ancestorUnresolvable(realRoot, dir, err)
		case !errors.Is(lerr, os.ErrNotExist):
			return ancestorUnresolvable(realRoot, dir, lerr)
		}
		// Only now is dir known to hold nothing at all, so there is nothing
		// about it to follow and the walk may go up.
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the filesystem root without finding anything that exists.
			//
			// Unreachable from any input this package can construct, and worth
			// stating so a mutation replacing this line — with a `return nil`,
			// even — is understood as an equivalence rather than as an untested
			// guard. full is always absolute: resolve refuses a root that is not,
			// and filepath.Join keeps an absolute first element absolute. The
			// climb therefore terminates at "/", EvalSymlinks("/") always
			// succeeds, and the loop has already returned under()'s answer before
			// parent can equal dir.
			//
			// Kept because "unreachable" is a claim about today's callers rather
			// than about this loop — the same reason under()'s Rel error is kept
			// — and because the alternative to refusing is treating a walk that
			// found nothing as containment, which is the one outcome this
			// function exists to prevent.
			return errors.New("no part of the path is on disk")
		}
		dir = parent
	}
}

// ancestorUnresolvable is the decision taken when a lookup about an ancestor
// fails for a reason of its own: containment is refused, never skipped past.
//
// A named function rather than an inline return because the branch that calls
// it — the Lstat failing non-ENOENT — is not reachable from any static tree, so
// nothing else can pin its direction. filepath.walkSymlinks returns os.Lstat's
// error verbatim, so an ENOENT out of EvalSymlinks(dir) is an Lstat ENOENT on
// dir or a prefix of it; by the time the walk asks about dir itself, Lstat
// agrees and the skip is taken. Only a race between the two calls separates
// them. The direction still has to be right, and here it can be stated and
// tested on its own.
//
// The failing error is deliberately not wrapped — see relativeTo. Its text is
// the absolute path being removed from these messages.
func ancestorUnresolvable(realRoot, dir string, _ error) error {
	return fmt.Errorf("cannot resolve an ancestor: %s", relativeTo(realRoot, dir))
}

// unreadableLinkRefusal is the decision taken when an ancestor answers as a
// symlink and then will not say where it points: where the chain ends is
// unknown, and unknown is not contained.
//
// Named for the same reason as ancestorUnresolvable — the branch needs Lstat and
// Readlink to disagree about one name, which only a race produces, so nothing
// reachable from a static tree can pin its direction.
func unreadableLinkRefusal(realRoot, dir string) error {
	return fmt.Errorf("an ancestor is a symlink that cannot be read: %s", relativeTo(realRoot, dir))
}

// relativeTo names an ancestor the way every other diagnostic in this module
// names a path: relative to the repository root, never absolutely.
//
// The absolute spelling is what the wrapped *PathErrors used to put in these
// messages, and it is both the spelling no hook can use and a disclosure of the
// filesystem layout around the repository. The underlying syscall error is
// dropped rather than wrapped, because for these branches its text is exactly
// the absolute path being removed — there is no part of it left to keep.
//
// A path that is not under the root has no relative name worth printing, and
// falling back to the absolute one would leak on precisely the inputs this
// exists to sanitise. Such an ancestor is described rather than named.
func relativeTo(realRoot, dir string) string {
	rel, err := filepath.Rel(realRoot, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "an ancestor above the repository root"
	}
	return rel
}

// danglingLinkStaysInside judges a dangling symlink ancestor by what it points
// at, since EvalSymlinks cannot and os.Readlink can.
//
// The link's target is resolved the way the kernel would — a relative target
// against the link's own directory — then cleaned and compared against the
// root. Cleaning is what makes the comparison meaningful for a path that does
// not exist: there is no tree to walk, so "repo/generated/../../outside" has to
// fold before under() sees it, and folding is exactly what the kernel would NOT
// do. That mismatch is the same one resolve documents, and it points the safe
// way here: the folded target is the shortest reading of where the link goes,
// so a target that escapes after folding escapes under any reading.
//
// The link's own name is what a producer needs and it is inside the repository,
// so it is the only thing named. The TARGET is never printed on refusal: it is
// the one string here that is both outside the repository and chosen by whoever
// made the link, and printing it would make the log the reward for the escape.
func danglingLinkStaysInside(realRoot, dir string) error {
	// A dangling link may point at ANOTHER dangling link, so the chain is
	// followed to its end rather than judged one hop deep. Stopping at the first
	// target reads "hop -> repo/escape" as inside — which it lexically is — while
	// "escape" itself points out of the repository, so the escape is reached by
	// one extra indirection and the check waves it through. Each hop is resolved
	// the way the kernel would, and the judgement is made on where the chain
	// actually ENDS.
	target := dir
	for hops := 0; ; hops++ {
		if hops > maxSymlinkHops {
			// A loop, or a chain long enough to be indistinguishable from one.
			// Where it ends is not knowable, so it is refused.
			return fmt.Errorf("an ancestor is a symlink that does not settle: %s", relativeTo(realRoot, dir))
		}
		info, lerr := os.Lstat(target)
		if lerr != nil {
			// The chain has run off the end of the tree: nothing is at this name,
			// which is the ordinary case — a link to a target not created yet.
			// Where that name SITS is what containment is about, so the loop stops
			// and the judgement is made on it.
			//
			// An Lstat failing for any other reason has established nothing, and
			// unestablished containment is refused, as everywhere else here.
			//
			// Removing this branch changes no verdict, and the reason is worth
			// recording so it is not mistaken for an untested guard: a name that
			// Lstat cannot read is a name resolveAsFarAsItGoes cannot resolve
			// either, so the comparison below places it above the root and
			// refuses it anyway. The branch is kept for the message and the
			// intent — refusing HERE says the chain could not be followed, while
			// falling through says the chain ended outside, and only the first is
			// true. A test cannot tell them apart through resolve; see
			// TestResolve_AChainEndingSomewhereUnreadableIsRefused, which pins the
			// outcome both routes share.
			if !errors.Is(lerr, os.ErrNotExist) {
				return ancestorUnresolvable(realRoot, dir, lerr)
			}
			break
		}
		if info.Mode()&os.ModeSymlink == 0 {
			// A real object, and the end of the chain.
			break
		}
		next, err := os.Readlink(target)
		if err != nil {
			// It answered as a link and will not say where it points. Refused.
			//
			// The choice between this refusal and ancestorUnresolvable's is
			// EQUIVALENT for everything a test can observe: both refuse, both name
			// the same ancestor relatively, both disclose nothing outside the
			// repository. Only the sentence differs, and no caller reads the
			// sentence. Recorded so a mutation swapping them is understood as an
			// equivalence rather than as this branch being untested.
			//
			// The branch itself needs Lstat to answer "symlink" and Readlink to
			// then fail on that same name, which only a race produces, so no
			// static tree reaches it. What CAN be pinned is the DIRECTION, and
			// TestResolve_ALinkThatWillNotSayWhereItPointsIsRefused pins it by
			// calling the decision directly.
			//
			// The distinct wording is kept for the reader rather than for a test:
			// "a symlink that cannot be read" says the chain was followed and one
			// link would not answer, where "cannot resolve an ancestor" says the
			// lookup never got that far. Only the first is true here.
			return unreadableLinkRefusal(realRoot, dir)
		}
		if !filepath.IsAbs(next) {
			next = filepath.Join(filepath.Dir(target), next)
		}
		target = filepath.Clean(next)
	}
	// The target is compared in the same terms as the root, which means resolving
	// it. Comparing a raw target against the resolved root is the very false
	// positive the root is resolved to avoid — on darwin /var is a link to
	// /private/var, so an entirely ordinary link into the repository would be
	// called an escape and the change would go silent.
	//
	// The target itself does not exist, so what is resolved is as much of it as
	// does; the remainder only ever DESCENDS from that, by the same argument the
	// walk above rests on, so it cannot re-enter the repository nor leave it.
	if err := under(realRoot, resolveAsFarAsItGoes(filepath.Clean(target))); err != nil {
		return fmt.Errorf("an ancestor is a symlink pointing outside the repository: %s", relativeTo(realRoot, dir))
	}
	return nil
}

// resolveAsFarAsItGoes resolves the longest prefix of path that is on disk and
// re-joins the rest onto it.
//
// Used for a link's target, which by definition may not exist yet: EvalSymlinks
// answers about nothing in that case, and a raw path cannot be compared against
// a resolved root. Rejoining the missing remainder is sound for the containment
// question because the remainder is cleaned and therefore only descends — it
// cannot leave a directory that is contained, nor re-enter one that is not.
//
// If nothing on the path resolves, the cleaned path is returned as it stands:
// there is nothing to learn from the filesystem, and the lexical comparison is
// then the whole answer.
func resolveAsFarAsItGoes(path string) string {
	remainder := ""
	for dir := path; ; {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(real, remainder)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return path
		}
		remainder = filepath.Join(filepath.Base(dir), remainder)
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
// Comparison is case-sensitive. Folding case would be wrong on a
// case-SENSITIVE filesystem, where "repo" and "REPO" are two directories and
// folding would let one pass for the other — so case-sensitive is the direction
// taken, and on a case-sensitive filesystem it is simply correct.
//
// On a case-INSENSITIVE one (darwin's default) it is not the safe conservative
// choice an earlier version of this comment claimed. "It errs toward refusing,
// so nothing outside the repository is let through by it" describes a
// disagreement between the root's spelling and a resolved path's, and that
// disagreement does not arise: EvalSymlinks does not normalise case, it echoes
// the spelling it was given, so both sides of the comparison carry the caller's
// own casing and under() agrees with itself.
//
// What actually happens is the opposite failure, and it is not under()'s to
// fix. One on-disk file repo/Dir/a.md answers to "dir/a.md", "DIR/a.md" and
// "dIr/A.MD", each of which resolves, stats present, and is contained — so one
// file yields four distinct event paths. That defeats the canonical-spelling
// guarantee the dedupe and ErrBaselineKeyedOnRawSpelling both rest on: the
// dedupe keys on a spelling the filesystem considers equal to three others, and
// a baseline keyed on any one of them disagrees with the rest.
//
// It is left alone deliberately. Canonicalising case means asking the
// filesystem for each element's true name, which is a per-element walk of
// exactly the TOCTOU-bound shape contained already is, on every path — and it
// would be wrong on a case-sensitive filesystem, where the four spellings are
// four different files and collapsing them would hide three real changes. The
// cost is bounded and does not point outward: every one of those spellings
// names the same file INSIDE the repository, so what a duplicate produces is a
// repeated event, never an escape. A producer that hands over git's own output
// hands over one spelling and never meets this.
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
