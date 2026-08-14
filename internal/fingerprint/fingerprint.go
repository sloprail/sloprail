// Package fingerprint answers what a file's content is, as one short string.
//
// It derives from the bytes and from nothing else. A file reverted to something
// already judged has not become new again, and one moved to another path has
// not become the same file there — a fingerprint taken from a timestamp, a size
// or a path would get both of those backwards.
//
// The scheme is git's own object hash. Not because anything here shells out to
// git, but because the tree this runs over is a git tree: reusing what git
// already computes means a fingerprint can be compared against something git
// reported without a second scheme having to be kept in step with the first.
package fingerprint

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"
)

// Of returns the fingerprint of content held in memory.
//
// git's blob hash: sha1 over the header "blob <length>\0" followed by the
// bytes. The length in the header is what keeps this from being a bare content
// hash — it is what makes the result the same string `git hash-object` prints.
func Of(content []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d", len(content))
	h.Write([]byte{0})
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// OfFile returns the fingerprint of what is on disk at path.
//
// Streamed rather than read whole, because the size is knowable up front from
// the stat that has to happen anyway and a file being judged may be large. A
// path that is not a regular file — missing, a directory, a device — has no
// content to fingerprint, and says so rather than returning a hash of nothing:
// a caller handed "" for a file it could not read would compare it against the
// next unreadable file and find them equal.
//
// The regular-file check happens TWICE, and the first one is not redundant with
// the second. It has to precede the open, because for two of the very kinds
// this function claims to refuse, open() itself is the thing that never
// returns: a FIFO blocks until some other process opens the write end, and so
// does a tty-like device. A check placed only after the open is code that runs
// on every path except the ones it was written for — the guard reads as present
// and the process hangs on the syscall above it, forever, holding the cycle it
// was called from. That is worse than the wrong answer it was preventing: a
// hook that returns nothing at least returns.
//
// O_NONBLOCK would be the other way to stop the block, and it is rejected: it
// changes the semantics of the read for every regular file to buy a property
// one stat already gives, and a FIFO opened non-blocking still is not something
// with content to fingerprint.
//
// The post-open check is kept because the pre-open one is a TOCTOU claim about
// a moment that has passed. Between the stat and the open the name can be
// swapped for a FIFO, and then only the second check sees it — too late to stop
// the block, but the block is now bounded by whatever the swapper does, and the
// verdict is still right. The two together mean the ordinary refusals never
// reach open(), and a race cannot make the ANSWER wrong.
//
// One consequence is worth recording so it is not read as a gap: the post-open
// check is now UNREACHABLE from any static tree, because the pre-open one
// answers first for every non-regular path that is not being swapped underneath
// us. It showed as covered before this guard existed — the directory test
// reached it — and shows as uncovered after, and that is the guard working
// rather than a test having been dropped. Only a race reaches it, so no test
// here claims to, and a mutation deleting it survives the suite.
func OfFile(path string) (string, error) {
	// Stat, not Lstat, and the difference decides a symlink's answer. What a
	// caller means by the fingerprint of a link is its TARGET's content — that
	// is what the open below would read — so the check has to judge the same
	// object the open will. Lstat would report every symlink as non-regular and
	// refuse a link to an ordinary file, which is a file with content and a
	// fingerprint. Following is also what catches the case this guard exists
	// for: a link whose target is a FIFO blocks exactly as the FIFO does.
	if info, err := os.Stat(path); err == nil && !info.Mode().IsRegular() {
		return "", fmt.Errorf("fingerprint: %s is not a regular file", path)
	}
	// A Stat that FAILED is not refused here. It is left to the open, which
	// produces the better message for the ordinary missing-file case and which
	// is the error every existing caller already distinguishes on.

	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("fingerprint: open %s: %w", path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("fingerprint: stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("fingerprint: %s is not a regular file", path)
	}

	h := sha1.New()
	io.WriteString(h, "blob "+strconv.FormatInt(info.Size(), 10))
	h.Write([]byte{0})
	n, err := io.Copy(h, f)
	if err != nil {
		return "", fmt.Errorf("fingerprint: read %s: %w", path, err)
	}
	if n != info.Size() {
		// The header committed to a length before the bytes were read, so a
		// file that changed underneath produces a hash matching neither what
		// was there nor what is. Better to have no fingerprint than a wrong one:
		// a wrong one that happens to match a stored pass exempts the file.
		return "", fmt.Errorf("fingerprint: %s changed while being read (%d bytes, expected %d)", path, n, info.Size())
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
