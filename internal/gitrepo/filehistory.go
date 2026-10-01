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
	rng := base + ".." + head
	if base == EmptyTree {
		rng = head
	}
	out, err := run(dir, "log", "--topo-order", "-M", "--cc", "--name-status", "-z", "--no-ext-diff", "--format=%x01%H", rng)
	if err != nil {
		return nil, fmt.Errorf("gitrepo: file history %s..%s: %w", short(base), short(head), err)
	}
	commits, err := parseNameStatusLog(out)
	if err != nil {
		return nil, err
	}

	result := make(map[string][]string, len(files))
	for _, f := range files {
		names := map[string]bool{f.Path: true}
		if f.OldPath != "" {
			names[f.OldPath] = true
		}
		var newestFirst []string
		for _, c := range commits {
			touched := false
			for _, e := range c.entries {
				switch {
				case e.status == 'R' || e.status == 'C':
					if names[e.path] {
						touched = true
						if e.status == 'R' {
							delete(names, e.path)
						}
						names[e.oldPath] = true
					}
				case names[e.path]:
					touched = true
					if e.status == 'A' {
						// Before it was added the file did not exist under this name.
						delete(names, e.path)
					}
				}
			}
			if touched {
				newestFirst = append(newestFirst, c.sha)
			}
		}
		oldest := make([]string, len(newestFirst))
		for i, sha := range newestFirst {
			oldest[len(newestFirst)-1-i] = sha
		}
		result[f.Path] = oldest
	}
	return result, nil
}

type logEntry struct {
	status  byte
	path    string
	oldPath string
}

type logCommit struct {
	sha     string
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
		if !isObjectName(fields[0]) {
			return nil, fmt.Errorf("gitrepo: unreadable commit name %q in the file history", fields[0])
		}
		c := logCommit{sha: fields[0]}
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
