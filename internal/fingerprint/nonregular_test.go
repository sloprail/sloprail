package fingerprint

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkfifo makes a named pipe, or says why it could not.
//
// A FIFO is the cheapest object whose open() blocks, which is the property
// these tests are about — not anything particular to pipes.
func mkfifo(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("cannot create a FIFO here: %v", err)
	}
}

// withinDeadline runs fn and reports whether it returned in time.
//
// The goroutine is deliberately abandoned on timeout rather than waited for:
// the failure being tested for is a call that never returns, so there is
// nothing to wait for, and the test's job is to fail rather than to hang with
// it. It leaks for the remainder of the process, which is the correct trade in
// a test binary that is about to fail anyway.
func withinDeadline(t *testing.T, d time.Duration, fn func()) bool {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// TestOfFile_AFifoIsRefusedRatherThanBlockedOn is a real defect, found here.
//
// OfFile's own documentation says a path that is not a regular file — and it
// names a DEVICE among them — "says so rather than returning a hash of
// nothing". It did not. The regular-file check sat AFTER os.Open, and open() on
// a FIFO blocks until another process opens the write end. Nothing ever does,
// so the check never ran and the call never returned: not a wrong fingerprint,
// no fingerprint and no error and no cycle either, the calling hook wedged for
// as long as the process lives.
//
// It is reachable from live code. sr-session's revalidation calls OfFile on
// every PostFileCreate and PostFileUpdate path, and the path comes from the
// tree — so one FIFO anywhere a cycle touches hangs the turn. A guardrail
// engine that stops answering is the silence this codebase is written against,
// arrived at through a syscall instead of a dropped event.
//
// The deadline is generous on purpose. The distinction being drawn is between
// "returns" and "never returns", not between fast and slow, so a second is
// already three orders of magnitude past what a refusal costs and cannot flake
// on a loaded machine.
func TestOfFile_AFifoIsRefusedRatherThanBlockedOn(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	mkfifo(t, fifo)

	var fp string
	var err error
	returned := withinDeadline(t, time.Second, func() { fp, err = OfFile(fifo) })

	require.True(t, returned,
		"OfFile blocked on a FIFO: open() waits for a writer, so a check placed after it never runs and the cycle never ends")
	require.Error(t, err)
	assert.Empty(t, fp, "a FIFO has no content to fingerprint, so it must yield none")
	assert.Contains(t, err.Error(), "not a regular file",
		"the reason must be what the object IS, which is the fact the caller can act on")
}

// TestOfFile_ASymlinkToAFifoIsRefusedToo closes the way around the guard above.
//
// The pre-open check follows the link on purpose — see OfFile — so it must
// refuse a link whose TARGET is a FIFO, which blocks exactly as the FIFO does.
// A guard written with Lstat would pass this link straight through to the open
// (a symlink is not a regular file, so Lstat's answer is "refuse", which sounds
// safe) and would ALSO refuse every ordinary link to an ordinary file, which is
// the case below. One check has to get both right, and only Stat does.
func TestOfFile_ASymlinkToAFifoIsRefusedToo(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	mkfifo(t, fifo)
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(fifo, link))

	var err error
	returned := withinDeadline(t, time.Second, func() { _, err = OfFile(link) })

	require.True(t, returned, "a symlink to a FIFO blocks in open() exactly as the FIFO does")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a regular file")
}

// TestOfFile_ASymlinkToARegularFileIsItsTargetsContent is the other half of the
// Stat-versus-Lstat choice, and it is what stops the fix above from being
// bought with a new silence.
//
// A link to an ordinary file HAS content — the target's, which is what the open
// reads — so it has a fingerprint, and it must be the target's. Refusing it
// would mean revalidation could not fingerprint any file reached through a
// link, so every such file would look like it had no identity to compare and
// would be re-judged forever.
func TestOfFile_ASymlinkToARegularFileIsItsTargetsContent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	require.NoError(t, os.WriteFile(target, []byte("body\n"), 0o644))
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(target, link))

	viaLink, err := OfFile(link)
	require.NoError(t, err, "a link to a regular file is a file with content")
	assert.Equal(t, Of([]byte("body\n")), viaLink,
		"and its fingerprint is its target's content, since that is what a read of it returns")
}

// TestOfFile_ABrokenSymlinkIsAnErrorNotAnEmptyHash. The pre-open Stat FAILS
// here rather than answering, which is why that failure is passed through to
// the open rather than refused on the spot: the open produces the error naming
// the missing file, which is the same error an ordinary missing path gives, and
// a caller distinguishing "nothing to fingerprint" from "not a file" gets the
// answer that is true.
func TestOfFile_ABrokenSymlinkIsAnErrorNotAnEmptyHash(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(filepath.Join(dir, "never-created"), link))

	fp, err := OfFile(link)
	require.Error(t, err, "a link to nothing has no content, and \"\" would compare equal to the next such link")
	assert.Empty(t, fp)
}

// TestOfFile_TwoPathsHoldingTheSameBytesFingerprintTheSame is the package's
// central claim stated over FILES rather than over byte slices, which is the
// form every caller uses.
//
// The existing TestOf_DerivesFromContentAlone makes the claim about Of. It is
// not the same claim: OfFile takes a path, and a fingerprint that let anything
// about the path leak in — the name, the inode, the mtime — would break exactly
// the comparison revalidation depends on, where a file moved or copied must be
// recognised as content already judged.
func TestOfFile_TwoPathsHoldingTheSameBytesFingerprintTheSame(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	b := filepath.Join(dir, "nested", "b.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(b), 0o755))
	require.NoError(t, os.WriteFile(a, []byte("identical"), 0o644))
	// A different mode and a later mtime, so that anything but the bytes
	// leaking into the hash would show up here.
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, os.WriteFile(b, []byte("identical"), 0o600))

	fa, err := OfFile(a)
	require.NoError(t, err)
	fb, err := OfFile(b)
	require.NoError(t, err)
	assert.Equal(t, fa, fb,
		"a fingerprint is of the content and of nothing else — a file copied elsewhere is not new content")
}

// TestOfFile_AnEmptyFileHasTheEmptyBlobFingerprint pins the boundary case the
// streaming path is most likely to get wrong: the header commits to length 0
// and io.Copy moves no bytes, so a hash built without writing the header at all
// would still look plausible.
//
// The expected value is git's own empty-blob hash, which is a constant every
// git user has seen, so this does not merely agree with Of's arithmetic.
func TestOfFile_AnEmptyFileHasTheEmptyBlobFingerprint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.md")
	require.NoError(t, os.WriteFile(path, nil, 0o644))

	fp, err := OfFile(path)
	require.NoError(t, err, "an empty file is a regular file and has content — none of it")
	assert.Equal(t, "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391", fp,
		"git's empty blob, which is what this scheme claims to be")
	assert.NotEmpty(t, fp,
		"and it must not be the empty string, which is what an unreadable file returns")
}

// TestOfFile_AnEmptyFileIsNotTheSameAsAnUnreadableOne is the reason the test
// above asserts NotEmpty as well.
//
// OfFile signals "no fingerprint" with "" and an error. An empty file has a
// real fingerprint, and if the two collided then every unreadable file would
// compare equal to every empty one — the exemption OfFile's doc is written
// against, reached from the one input where "" is the honest description of the
// content.
func TestOfFile_AnEmptyFileIsNotTheSameAsAnUnreadableOne(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.md")
	require.NoError(t, os.WriteFile(empty, nil, 0o644))

	emptyFP, err := OfFile(empty)
	require.NoError(t, err)

	missingFP, err := OfFile(filepath.Join(dir, "absent.md"))
	require.Error(t, err)

	assert.NotEqual(t, emptyFP, missingFP,
		"a file with no bytes and a file that cannot be read must never compare equal")
}

// TestOfFile_AFileTooLargeToHoldInMemoryIsStillFingerprinted is the claim
// "streamed rather than read whole" being worth something.
//
// 64 MiB is far past any io.Copy buffer, so the header-then-stream path is
// exercised across many reads. Agreement with Of over the same bytes is the
// assertion: a streaming implementation that mishandled the boundary between
// reads would produce something self-consistent and wrong, which only a
// comparison against the whole-buffer answer catches.
func TestOfFile_AFileTooLargeToHoldInMemoryIsStillFingerprinted(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates 64 MiB twice")
	}
	// Not all one byte: a repeated byte hashes the same however the reads are
	// chunked, so a boundary bug would survive it.
	content := make([]byte, 64<<20)
	for i := range content {
		content[i] = byte(i * 7)
	}
	path := filepath.Join(t.TempDir(), "big.bin")
	require.NoError(t, os.WriteFile(path, content, 0o644))

	fp, err := OfFile(path)
	require.NoError(t, err)
	assert.Equal(t, Of(content), fp,
		"streaming a file must produce what hashing its bytes whole produces, at any size")
}

// TestOf_BinaryContentIsFingerprintedByItsBytes. The scheme is git's blob hash,
// which is defined over bytes and knows nothing about text — but "blob %d\0"
// followed by content puts a NUL in the stream, and an implementation that
// treated the content as a C string or a Go string boundary would stop at the
// first NUL IN THE CONTENT and hash a prefix.
//
// Two payloads differing only AFTER an embedded NUL is what makes that visible:
// they are the same length, so even the header matches, and only the bytes past
// the NUL tell them apart.
func TestOf_BinaryContentIsFingerprintedByItsBytes(t *testing.T) {
	a := []byte{0x89, 'P', 'N', 'G', 0x00, 0x01, 0x02}
	b := []byte{0x89, 'P', 'N', 'G', 0x00, 0x01, 0x03}

	assert.NotEqual(t, Of(a), Of(b),
		"content differing only past an embedded NUL is different content")

	dir := t.TempDir()
	pa, pb := filepath.Join(dir, "a.bin"), filepath.Join(dir, "b.bin")
	require.NoError(t, os.WriteFile(pa, a, 0o644))
	require.NoError(t, os.WriteFile(pb, b, 0o644))

	fa, err := OfFile(pa)
	require.NoError(t, err)
	fb, err := OfFile(pb)
	require.NoError(t, err)
	assert.NotEqual(t, fa, fb, "and the same must hold read from disk")
	assert.Equal(t, Of(a), fa, "the file's fingerprint is its bytes' fingerprint")
}

// TestOf_ATrailingNewlineIsContent. A file ending without one is the ordinary
// case for generated output, and it must not fingerprint as the same content as
// the same text with one — a rule that passed on "foo\n" has not been shown to
// pass on "foo".
func TestOf_ATrailingNewlineIsContent(t *testing.T) {
	assert.NotEqual(t, Of([]byte("body")), Of([]byte("body\n")),
		"the newline is a byte, and content is bytes")

	dir := t.TempDir()
	with, without := filepath.Join(dir, "w.md"), filepath.Join(dir, "wo.md")
	require.NoError(t, os.WriteFile(with, []byte("body\n"), 0o644))
	require.NoError(t, os.WriteFile(without, []byte("body"), 0o644))

	fw, err := OfFile(with)
	require.NoError(t, err)
	fwo, err := OfFile(without)
	require.NoError(t, err)
	assert.NotEqual(t, fw, fwo)
}
