package gitrepo

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// What changed between two commits, read as data.
//
// Everything here is strict where content.go and changed.go are forgiving. Those
// serve a cycle that would rather drop one unreadable blob than abort the rest;
// a changeset is what a rule is JUDGED on, so a file whose content or diff
// cannot be read is an error, never an empty string. An unreadable diff that
// reads as "no change" is how a10n promoted its base over a bad draft.

// Delta is one path that differs between two commits, in git's own terms.
type Delta struct {
	// Path is the path at the newer commit (the old one, for a deletion).
	Path string
	// OldPath is where a renamed file came from; empty otherwise.
	OldPath string
	// Status is one of 'A' added, 'M' modified, 'D' deleted, 'R' renamed. A type
	// change ('T', file to symlink) is reported as 'M'. Any other letter git
	// invents is an error, not a guess.
	Status byte
	// Gitlink marks a submodule pointer at either end. It has no readable content,
	// only a commit name in its diff.
	Gitlink bool
	// OldBlob and NewBlob are the object ids git names for the two sides (all zeros for an absent
	// side): the content hash, with no read of the content.
	OldBlob, NewBlob string
}

// Deltas lists every path that differs between base and head, with renames
// detected (`-M`). The list is complete or it is an error: a git failure or a
// line this does not understand returns no partial answer.
func Deltas(dir, base, head string) ([]Delta, error) {
	out, err := runImmutable(dir, []string{base, head}, "diff", "-M", "--raw", "-z", "--no-abbrev", "--no-ext-diff", "--no-textconv", base, head)
	if err != nil {
		return nil, fmt.Errorf("gitrepo: diff %s..%s: %w", short(base), short(head), err)
	}
	return parseRaw(out)
}

// parseRaw reads `git diff --raw -z` output: a NUL-terminated header
// (`:<mode> <mode> <sha> <sha> <status>`) followed by one path — or two, for a
// rename — each NUL-terminated.
func parseRaw(out string) ([]Delta, error) {
	fields := strings.Split(out, "\x00")
	// A well-formed stream ends with a NUL, so the last field is empty.
	if n := len(fields); n > 0 && fields[n-1] == "" {
		fields = fields[:n-1]
	}
	var deltas []Delta
	for i := 0; i < len(fields); {
		header := fields[i]
		i++
		parts := strings.Fields(header)
		if len(parts) != 5 || !strings.HasPrefix(parts[0], ":") {
			return nil, fmt.Errorf("gitrepo: unreadable diff header %q", header)
		}
		d := Delta{Gitlink: parts[0] == ":160000" || parts[1] == "160000", OldBlob: parts[2], NewBlob: parts[3]}
		switch parts[4][0] {
		case 'A', 'M', 'D':
			d.Status = parts[4][0]
		case 'T':
			d.Status = 'M'
		case 'R':
			d.Status = 'R'
		default:
			return nil, fmt.Errorf("gitrepo: diff status %q is not one this understands", parts[4])
		}
		want := 1
		if d.Status == 'R' {
			want = 2
		}
		if i+want > len(fields) {
			return nil, fmt.Errorf("gitrepo: diff entry %q is missing its path", header)
		}
		if d.Status == 'R' {
			d.OldPath, d.Path = fields[i], fields[i+1]
		} else {
			d.Path = fields[i]
		}
		i += want
		if d.Path == "" {
			return nil, fmt.Errorf("gitrepo: diff entry %q has an empty path", header)
		}
		deltas = append(deltas, d)
	}
	return deltas, nil
}

// PatchOf is the unified diff of one delta between base and head, with rename
// detection on so a rename reads as a rename rather than a deletion plus an
// addition. The old path is named as well as the new one for exactly that reason.
func PatchOf(dir, base, head string, d Delta) (string, error) {
	if immutableRev.MatchString(base) && immutableRev.MatchString(head) {
		// Every file-guard of the range asks for the same patches: one git process each, not one per guard.
		return memoGit(blobKey{"patch", dir, base + ".." + head, d.Path + "\x00" + d.OldPath}, func() (string, error) { return patchOf(dir, base, head, d) })
	}
	return patchOf(dir, base, head, d)
}

func patchOf(dir, base, head string, d Delta) (string, error) {
	args := []string{"diff", "-M", "--no-ext-diff", "--no-textconv", "--no-color", base, head, "--", d.Path}
	if d.OldPath != "" {
		args = append(args, d.OldPath)
	}
	out, err := run(dir, args...)
	if err != nil {
		return "", fmt.Errorf("gitrepo: diff of %q: %w", d.Path, err)
	}
	return out, nil
}

// BlobAt reads a file as of a commit, as a checkout would write it, or fails.
//
// The strict twin of ContentAt: the caller has already decided the path SHOULD
// exist at that commit (it is in a delta), so a failure to read it is a fault
// and not an absence.
func BlobAt(dir, commit, path string) (string, error) {
	if !immutableRev.MatchString(commit) {
		return blobAt(dir, commit, path)
	}
	// A commit named by its sha never changes, and every file-guard of a range reads the same
	// blobs: one git process per (commit, path), not one per guard.
	return memoGit(blobKey{"blob", dir, commit, path}, func() (string, error) { return blobAt(dir, commit, path) })
}

// immutableRev is a revision that names one commit forever: a sha, or one of its parents.
var immutableRev = regexp.MustCompile(`^[0-9a-f]{40}(\^[0-9]*)?$`)

type blobKey struct{ kind, dir, a, b string }

type blobEntry struct {
	mu      sync.Mutex
	done    bool
	content string
	err     error
}

var blobMemo sync.Map

// runImmutable is run, remembered for the process when every revision in revs is a full object
// id (whose answer cannot change): the file-guards of a range ask the same questions of history.
func runImmutable(dir string, revs []string, args ...string) (string, error) {
	for _, r := range revs {
		if !immutableRev.MatchString(r) && r != EmptyTree {
			return run(dir, args...)
		}
	}
	key := blobKey{"run", dir, strings.Join(args, "\x00"), ""}
	v, _ := blobMemo.LoadOrStore(key, &blobEntry{})
	e := v.(*blobEntry)
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.done {
		e.content, e.err = run(dir, args...) // a failure of an immutable question is as final as an answer
		e.done = true
	}
	return e.content, e.err
}

// memoGit runs read once per key, concurrent askers waiting for the one run; a failure is not kept.
func memoGit(key blobKey, read func() (string, error)) (string, error) {
	v, _ := blobMemo.LoadOrStore(key, &blobEntry{})
	e := v.(*blobEntry)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.done {
		return e.content, nil
	}
	content, err := read()
	if err != nil {
		return "", err
	}
	e.content, e.done = content, true
	return content, nil
}

func blobAt(dir, commit, path string) (string, error) {
	out, err := run(dir, "cat-file", "--filters", fmt.Sprintf("%s:%s", commit, path))
	if err != nil {
		return "", fmt.Errorf("gitrepo: read %q at %s: %w", path, short(commit), err)
	}
	return out, nil
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
