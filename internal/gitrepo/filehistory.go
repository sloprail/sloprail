package gitrepo

import (
	"fmt"
	"strings"
)

// Which commits of a range changed a file.
//
// `git log --follow` is the obvious tool and is not usable here: it follows one
// path, and in a range it stops at the first rename it meets, so the commits
// after a rename were missing from its answer. The history is read once, for
// every commit of the range with renames detected, and each file's own names are
// followed through it.

// FileRef names a file of a range as its squashed delta does: the path it has at
// head (at base, for a deletion), and the path it came from when it was renamed.
type FileRef struct {
	Path    string
	OldPath string
}

// FileCommits maps each file's Path to the commits of base..head that changed
// it, oldest first: a commit that added, modified, deleted or renamed the file
// (or, for a merge, changed it in resolving the merge), following the file back
// through each rename inside the range to the name it had before.
//
// A merge commit that merely brings in a side branch changes nothing itself; the
// side branch's own commits are in the range and carry the change.
//
// Any git failure, or output this does not understand, is an error: the answer
// is complete or there is none.
func FileCommits(dir, base, head string, files []FileRef) (map[string][]string, error) {
	touches, err := fileTouches(dir, base, head, files)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]string, len(files))
	for p, ts := range touches {
		shas := make([]string, len(ts))
		for i, t := range ts {
			shas[i] = t.sha
		}
		result[p] = shas
	}
	return result, nil
}

// SubstantiveFileCommits is FileCommits keeping only the commits whose change to
// the file is more than whitespace: the file's content, with all whitespace
// stripped, differs from what it was in the commit's first parent. An added or
// deleted file, a root commit, and anything that cannot be read count as
// substantive (a change that cannot be shown to be whitespace is not waved
// through). A commit that only touches whitespace changes nothing a citation could
// ground, so it must neither need one nor lend one.
func SubstantiveFileCommits(dir, base, head string, files []FileRef) (map[string][]string, error) {
	touches, err := fileTouches(dir, base, head, files)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]string, len(files))
	for p, ts := range touches {
		shas := []string{}
		for _, t := range ts {
			if t.substantive(dir) {
				shas = append(shas, t.sha)
			}
		}
		result[p] = shas
	}
	return result, nil
}

// fileTouch is one commit's change to a file: its name in the commit and in the
// commit's first parent.
type fileTouch struct {
	sha, path, parentPath string
	status                byte
}

func (t fileTouch) substantive(dir string) bool {
	if t.status == 'A' || t.status == 'D' {
		return true
	}
	after, err := BlobAt(dir, t.sha, t.path)
	if err != nil {
		return true
	}
	before, err := BlobAt(dir, t.sha+"^1", t.parentPath)
	if err != nil {
		return true
	}
	return strings.Join(strings.Fields(before), "") != strings.Join(strings.Fields(after), "")
}

func fileTouches(dir, base, head string, files []FileRef) (map[string][]fileTouch, error) {
	rng := base + ".." + head
	if base == EmptyTree {
		rng = head
	}
	out, err := run(dir, "log", "--topo-order", "-M", "--cc", "--name-status", "-z", "--no-ext-diff", "--format=%x01%H %P", rng)
	if err != nil {
		return nil, fmt.Errorf("gitrepo: file history %s..%s: %w", short(base), short(head), err)
	}
	commits, err := parseNameStatusLog(out)
	if err != nil {
		return nil, err
	}

	result := make(map[string][]fileTouch, len(files))
	for _, f := range files {
		names := map[string]bool{f.Path: true}
		if f.OldPath != "" {
			names[f.OldPath] = true
		}
		var newestFirst []fileTouch
		for _, c := range commits {
			var touch *fileTouch
			for _, e := range c.entries {
				switch {
				case e.status == 'R' || e.status == 'C':
					if names[e.path] {
						touch = &fileTouch{sha: c.sha, status: e.status, path: e.path, parentPath: e.oldPath}
						if e.status == 'R' {
							delete(names, e.path)
						}
						names[e.oldPath] = true
					}
				case names[e.path]:
					touch = &fileTouch{sha: c.sha, status: e.status, path: e.path, parentPath: e.path}
					if e.status == 'A' {
						// Before it was added the file did not exist under this name.
						delete(names, e.path)
					}
				}
			}
			if touch != nil && len(c.parents) == 2 && touch.status == 'M' && cleanMergeOf(dir, c, touch.path) {
				touch = nil // a clean merge only carries what its sides changed
			}
			if touch != nil {
				newestFirst = append(newestFirst, *touch)
			}
		}
		oldest := make([]fileTouch, len(newestFirst))
		for i, sha := range newestFirst {
			oldest[len(newestFirst)-1-i] = sha
		}
		result[f.Path] = oldest
	}
	return result, nil
}

// cleanMergeOf reports whether merge commit c, for path, is exactly what the automatic
// merge of its two parents produces: it carried the two sides' changes and resolved
// nothing, so it did not change the file (the side commits did). A merge whose file
// differs from that (a conflict resolved by hand, an evil merge) did change it. Anything
// that cannot be established (an old git without `merge-tree --write-tree`, a conflicted
// automatic merge, a failed read) is false, so the merge still counts as the change.
func cleanMergeOf(dir string, c logCommit, path string) bool {
	out, err := run(dir, "merge-tree", "--write-tree", "--no-messages", c.parents[0], c.parents[1])
	if err != nil {
		return false
	}
	tree := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	if !isObjectName(tree) {
		return false
	}
	auto, err := run(dir, "rev-parse", "--verify", "-q", tree+":"+path)
	if err != nil {
		return false
	}
	merged, err := run(dir, "rev-parse", "--verify", "-q", c.sha+":"+path)
	if err != nil {
		return false
	}
	return strings.TrimSpace(auto) == strings.TrimSpace(merged)
}

type logEntry struct {
	status  byte
	path    string
	oldPath string
}

type logCommit struct {
	sha     string
	parents []string
	entries []logEntry
}

// parseNameStatusLog reads `git log --name-status -z --format=%x01%H`: per commit
// a SOH, its name and a NUL, then each change as a status, a NUL and its path
// (two, for a rename or a copy), each NUL-terminated.
func parseNameStatusLog(out string) ([]logCommit, error) {
	var commits []logCommit
	for _, chunk := range strings.Split(out, "\x01") {
		if chunk == "" {
			continue
		}
		fields := strings.Split(chunk, "\x00")
		names := strings.Fields(fields[0]) // the commit, then its parents
		if len(names) == 0 || !isObjectName(names[0]) {
			return nil, fmt.Errorf("gitrepo: unreadable commit name %q in the file history", fields[0])
		}
		c := logCommit{sha: names[0], parents: names[1:]}
		for i := 1; i < len(fields); {
			status := strings.TrimPrefix(fields[i], "\n")
			if status == "" {
				i++
				continue
			}
			e := logEntry{status: status[0]}
			if combinedStatus(status) {
				// A merge's combined diff (--cc) prints one letter per parent ("MM", "RM",
				// "AM") and ONE path, the file's name in the merge; the file changed in
				// resolving it, so it counts as modified under that name.
				e.status = 'M'
				if i+1 >= len(fields) {
					return nil, fmt.Errorf("gitrepo: file history entry %q is missing its path", status)
				}
				e.path = fields[i+1]
				i += 2
				c.entries = append(c.entries, e)
				continue
			}
			switch e.status {
			case 'A', 'M', 'D', 'T', 'U', 'X', 'B':
				if i+1 >= len(fields) {
					return nil, fmt.Errorf("gitrepo: file history entry %q is missing its path", status)
				}
				e.path = fields[i+1]
				i += 2
			case 'R', 'C':
				if i+2 >= len(fields) {
					return nil, fmt.Errorf("gitrepo: file history entry %q is missing a path", status)
				}
				e.oldPath, e.path = fields[i+1], fields[i+2]
				i += 3
			default:
				return nil, fmt.Errorf("gitrepo: file history status %q is not one this understands", status)
			}
			if e.path == "" || (e.status == 'R' || e.status == 'C') && e.oldPath == "" {
				return nil, fmt.Errorf("gitrepo: file history entry %q has an empty path", status)
			}
			c.entries = append(c.entries, e)
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// combinedStatus reports a status of a combined (merge) diff: two or more bare letters,
// one per parent, with no similarity score.
func combinedStatus(s string) bool {
	if len(s) < 2 {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}
