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
func OfFile(path string) (string, error) {
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
