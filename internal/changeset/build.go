package changeset

import (
	"fmt"

	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/transcript"
)

// DeletionMode is a file-guard's `deletions:`, read as a filter on a file's
// STATUS in the range. The zero value is Skip, the default.
type DeletionMode string

const (
	// SkipDeletions leaves deleted files out of `files` and lists them in `others`.
	SkipDeletions DeletionMode = "skip"
	// IncludeDeletions lets deleted files into `files` beside the rest.
	IncludeDeletions DeletionMode = "include"
	// OnlyDeletions keeps only deleted files in `files`.
	OnlyDeletions DeletionMode = "only"
)

// Scanner reads the markers out of a file's text.
type Scanner func(text string) []Marker

// Scope is what a rule's `match` is asked about one file: its path and status,
// the markers it carries and carried, and the trailers of the whole range.
// `context` is the caller's to add; it is not a fact about the range.
type Scope struct {
	Path       string
	Status     string
	Markers    []Marker
	OldMarkers []Marker
	Trailers   map[string][]string
}

// Options say how to build a changeset.
type Options struct {
	Deletions DeletionMode
	// Scan finds markers in a file's text. Required.
	Scan Scanner
	// Select is the rule's `match`. Required. An error is the caller's to fail
	// closed on; nothing here treats it as "not selected".
	Select func(Scope) (bool, error)
}

// Build reads the range and returns the changeset a rule is judged on.
//
// Every git failure returns an error and no changeset: a range that could not
// be read must never resemble one that had nothing selected. The latter is a
// Changeset with no Files, which the caller passes; the former is an error,
// which the caller refuses.
//
// Contents and diffs are read from the two commits and never from the working
// tree, so an uncommitted edit is invisible here, however close to done.
//
// Citations are not part of what Build knows; see ResolveCitations.
func Build(dir string, r gitrepo.Range, o Options) (Changeset, error) {
	if o.Scan == nil || o.Select == nil {
		return Changeset{}, fmt.Errorf("changeset: Scan and Select are required")
	}
	if r.Base == "" || r.Head == "" {
		return Changeset{}, fmt.Errorf("changeset: a range needs a base and a head")
	}
	deltas, err := gitrepo.Deltas(dir, r.Base, r.Head)
	if err != nil {
		return Changeset{}, err
	}
	gitCommits, err := gitrepo.CommitsIn(dir, r.Base, r.Head)
	if err != nil {
		return Changeset{}, err
	}
	commits := commitsOf(gitCommits)
	trailers := TrailerScope(commits)

	cs := Changeset{Base: r.Base, Head: r.Head, Commits: commits, Files: []File{}, Others: []Other{}, Citations: []transcript.Citation{}}
	for _, d := range deltas {
		status := string(d.Status)
		if !admits(o.Deletions, d.Status) {
			cs.Others = append(cs.Others, Other{Path: d.Path, Status: status})
			continue
		}
		f, err := readFile(dir, r, d, o.Scan)
		if err != nil {
			return Changeset{}, err
		}
		// A deleted file has no result, so the markers it is matched on are the
		// ones it carried.
		markers := f.NewMarkers
		if d.Status == 'D' {
			markers = f.OldMarkers
		}
		selected, err := o.Select(Scope{Path: f.Path, Status: status, Markers: markers, OldMarkers: f.OldMarkers, Trailers: trailers})
		if err != nil {
			return Changeset{}, fmt.Errorf("changeset: match on %q: %w", f.Path, err)
		}
		if !selected {
			cs.Others = append(cs.Others, Other{Path: d.Path, Status: status})
			continue
		}
		if f.Diff, err = gitrepo.PatchOf(dir, r.Base, r.Head, d); err != nil {
			return Changeset{}, err
		}
		cs.Files = append(cs.Files, f)
	}
	return cs, nil
}

// admits says whether a status may enter `files` at all under a deletions mode.
// A rename is not a deletion.
func admits(mode DeletionMode, status byte) bool {
	switch mode {
	case IncludeDeletions:
		return true
	case OnlyDeletions:
		return status == 'D'
	default:
		return status != 'D'
	}
}

// readFile reads a delta's two sides from the commits.
func readFile(dir string, r gitrepo.Range, d gitrepo.Delta, scan Scanner) (File, error) {
	f := File{Path: d.Path, Status: string(d.Status), OldPath: d.OldPath, OldMarkers: []Marker{}, NewMarkers: []Marker{}}
	oldPath := d.Path
	if d.OldPath != "" {
		oldPath = d.OldPath
	}
	if d.Status != 'A' && !d.Gitlink {
		content, err := gitrepo.BlobAt(dir, r.Base, oldPath)
		if err != nil {
			return File{}, err
		}
		f.OldContent = content
		f.OldMarkers = markersOf(scan, content)
	}
	if d.Status != 'D' && !d.Gitlink {
		content, err := gitrepo.BlobAt(dir, r.Head, d.Path)
		if err != nil {
			return File{}, err
		}
		f.NewContent = content
		f.NewMarkers = markersOf(scan, content)
	}
	return f, nil
}

// markersOf is scan's answer, never nil: a list that JSON renders as [] so a
// check reading `.oldMarkers[]` never meets null.
func markersOf(scan Scanner, text string) []Marker {
	if m := scan(text); m != nil {
		return m
	}
	return []Marker{}
}
