package gitrepo

import (
	"fmt"
	"strings"
)

// Uncommitted is one path whose working tree or index differs from HEAD: the
// work a file-guard cannot judge yet, because it judges commits.
type Uncommitted struct {
	Path string
	// OldPath is where a renamed file came from; empty otherwise.
	OldPath string
	// Status is 'A' (new to HEAD, staged or not), 'M', 'D' (gone from the working
	// tree or index, present at HEAD) or 'R'.
	Status byte
}

// UncommittedChanges lists every path that differs from HEAD, staged, unstaged
// or untracked — the same four kinds of work Changed reports, but with the status
// a rule's `deletions:` needs and without measuring against any earlier commit.
//
// Ignored files are not listed (they are not the repository's to commit), an
// untracked nested repository is not either, and a
// submodule is one path that counts only when its pointer moved. A path added and
// then deleted again before ever being committed differs from HEAD in nothing and
// is not listed.
//
// Any failure is an error: a status that could not be read must not read as a
// clean tree.
func UncommittedChanges(dir string) ([]Uncommitted, error) {
	out, err := run(dir, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=dirty")
	if err != nil {
		return nil, err
	}
	fields := strings.Split(out, "\x00")
	var changes []Uncommitted
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if entry == "" {
			continue
		}
		if len(entry) < 4 || entry[2] != ' ' {
			return nil, fmt.Errorf("gitrepo: unreadable status entry %q", entry)
		}
		x, y, path := entry[0], entry[1], entry[3:]
		c := Uncommitted{Path: path}
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			// A rename or copy carries the source as the next field.
			if i+1 >= len(fields) || fields[i+1] == "" {
				return nil, fmt.Errorf("gitrepo: status entry %q is missing its source path", entry)
			}
			i++
			c.OldPath = fields[i]
		}
		switch {
		case x == '?' && y == '?' && strings.HasSuffix(path, "/"):
			// An untracked NESTED REPOSITORY (a clone the agent made to look at):
			// git lists it as its directory, and it is not this repository's to
			// commit — its contents never reach a rule. Left out, as Changed leaves
			// it out. One that is TRACKED (a submodule) is a gitlink entry and still
			// counts when its pointer moves.
			continue
		case x == '?' && y == '?':
			c.Status = 'A'
		case x == 'A' && y == 'D':
			continue // never committed, already gone: nothing differs from HEAD
		case y == 'D', x == 'D' && y == ' ':
			c.Status = 'D'
		case x == 'R', y == 'R':
			c.Status = 'R'
		case x == 'C', y == 'C', x == 'A', y == 'A':
			c.Status = 'A'
		default:
			c.Status = 'M'
		}
		changes = append(changes, c)
	}
	return changes, nil
}
