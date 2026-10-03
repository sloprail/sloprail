package gitrepo

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every status git prints in `log --name-status -z` is read, and the path fields that
// follow it are consumed in the right number, so the next entry is never read as a status.
func TestParseNameStatusLog_EveryStatus(t *testing.T) {
	sha := EmptyTree
	cases := []struct {
		name   string
		fields string // after the commit name
		want   []logEntry
	}{
		{"added", "A\x00a\x00", []logEntry{{status: 'A', path: "a"}}},
		{"modified", "M\x00a\x00", []logEntry{{status: 'M', path: "a"}}},
		{"deleted", "D\x00a\x00", []logEntry{{status: 'D', path: "a"}}},
		{"type change", "T\x00a\x00", []logEntry{{status: 'T', path: "a"}}},
		{"unmerged", "U\x00a\x00", []logEntry{{status: 'U', path: "a"}}},
		{"unknown", "X\x00a\x00", []logEntry{{status: 'X', path: "a"}}},
		{"rename with score", "R100\x00old\x00new\x00", []logEntry{{status: 'R', oldPath: "old", path: "new"}}},
		{"copy with score", "C075\x00old\x00new\x00", []logEntry{{status: 'C', oldPath: "old", path: "new"}}},
		{"combined merge statuses", "MM\x00a\x00RM\x00b\x00AM\x00c\x00", []logEntry{
			{status: 'M', path: "a"}, {status: 'M', path: "b"}, {status: 'M', path: "c"}}},
		{"a copy then a type change then a modify", "C075\x00o\x00n\x00T\x00t\x00M\x00m\x00", []logEntry{
			{status: 'C', oldPath: "o", path: "n"}, {status: 'T', path: "t"}, {status: 'M', path: "m"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			commits, err := parseNameStatusLog("\x01" + sha + "\x00\n" + tc.fields)
			require.NoError(t, err)
			require.Len(t, commits, 1)
			assert.Equal(t, tc.want, commits[0].entries)
		})
	}
}

// A real merge: `--cc` prints "RM" with ONE path; reading it as a rename (two paths)
// used to swallow the next entry's status and fail the whole evaluation.
func TestFileCommits_ReadsAMergeWithACombinedRename(t *testing.T) {
	dir := initRepo(t)
	commitFile(t, dir, "f.txt", "a\nb\nc\nd\ne\nf\ng\n", "base")
	commitFile(t, dir, "r.txt", "1\n2\n3\n4\n5\n6\n", "base r")
	git(t, dir, "switch", "-qc", "side")
	git(t, dir, "mv", "r.txt", "r2.txt")
	commitFile(t, dir, "f.txt", "a\nB\nc\nd\ne\nf\ng\n", "side")
	git(t, dir, "switch", "-q", "main")
	commitFile(t, dir, "r.txt", "1\n2\n3\n4\n5\n6\n7\n", "main r")
	commitFile(t, dir, "f.txt", "a\nX\nc\nd\ne\nf\ng\n", "main")
	_ = exec.Command("git", "-C", dir, "merge", "side").Run()
	commitFile(t, dir, "f.txt", "a\nM\nc\nd\ne\nf\ng\n", "merge")
	base := git(t, dir, "rev-parse", "HEAD~3")
	_, err := FileCommits(dir, base, "HEAD", []FileRef{{Path: "f.txt"}, {Path: "r2.txt"}})
	assert.NoError(t, err)
}
