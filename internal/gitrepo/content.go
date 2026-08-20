package gitrepo

import "fmt"

// ContentAt reads a file's bytes as of a commit, and reports whether they could
// be read at all.
//
// This is the half of a Post file event the working tree cannot supply. What a
// file holds NOW is a read off disk; what it held at the point a cycle measures
// from is a fact about a commit the tree may since have left behind, and only
// git still has it. filemod.Observed carries it across the boundary so the
// module — which does not read git — can put `oldContent` on a PostFileUpdate or
// PostFileDelete.
//
// The path is repository-relative, in git's own spelling — the same spelling
// Changed reports — and it is read from the commit's tree with `git show
// <commit>:<path>`. A blob that git prints is returned exactly as git prints it;
// git does not add or strip a trailing newline of its own, so the bytes are the
// file's.
//
// The boolean separates "the blob is empty" from "there is no blob to read".
// A file that was genuinely empty at the baseline returns ("", true); a path git
// cannot resolve at that commit — it was not tracked there, or the commit itself
// is unreadable — returns ("", false). A rule about what a change removed needs
// the two told apart, since "" is a legitimate prior content and its absence is
// not, so a caller must not read the empty string as the file's content when the
// read failed.
//
// A read that fails is NOT an error the caller has to handle: it is the ordinary
// case for a path that was not in the commit (a create has no baseline content,
// though a create is never asked, since it declares no oldContent). Reporting it
// as false rather than as a returned error keeps a single unreadable blob from
// having to abort the classification of every other file in the same cycle.
func ContentAt(dir, commit, path string) (string, bool) {
	if commit == "" || path == "" {
		return "", false
	}
	// `git show <commit>:<path>` prints the blob at that path in that commit.
	// The `<rev>:<path>` form is unambiguous — there is no pathspec parsing to be
	// confused by a leading dash — so no `--` separator is needed or accepted.
	out, err := run(dir, "show", fmt.Sprintf("%s:%s", commit, path))
	if err != nil {
		return "", false
	}
	return out, true
}
