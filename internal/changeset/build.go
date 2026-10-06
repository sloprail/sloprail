package changeset

import (
	"fmt"

	"github.com/sloprail/sloprail/internal/gitrepo"
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
	Path string
	// OldPath is where a renamed file came from; empty otherwise. Not part of
	// what `match` reads: it is what Selects falls back on for a rename.
	OldPath    string
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
	// Lean builds only what a verdict KEY is over, which is the files' git blob ids and paths:
	// no file is read, no patch made, no marker scanned (a `match` that reads markers is not
	// lean). It is what `verify` uses: it executes nothing, so nothing needs the bytes.
	Lean bool
	// NoPatch leaves each file's patch (Diff) out: not part of any key, and a verify never shows it.
	NoPatch bool
	// RawBlobs reads file text by blob id, in one long-lived git process, without attribute
	// filters: for a caller that only scans it (markers), never hands it to a check.
	RawBlobs bool
	// SkipHistory leaves out which commits changed each file; only a citation requirement
	// reads it.
	SkipHistory bool
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

	cs := Changeset{Base: r.Base, Head: r.Head, Commits: commits, Files: []File{}, Others: []Other{}, Citations: []Citation{}}
	for _, d := range deltas {
		status := string(d.Status)
		if !Admits(o.Deletions, d.Status) {
			cs.Others = append(cs.Others, Other{Path: d.Path, Status: status})
			continue
		}
		f, err := readFile(dir, r, d, o.Scan, o.Lean, o.RawBlobs)
		if err != nil {
			return Changeset{}, err
		}
		// A deleted file has no result, so the markers it is matched on are the
		// ones it carried.
		markers := f.NewMarkers
		if d.Status == 'D' {
			markers = f.OldMarkers
		}
		selected, err := Selects(o.Select, Scope{Path: f.Path, OldPath: f.OldPath, Status: status, Markers: markers, OldMarkers: f.OldMarkers, Trailers: trailers})
		if err != nil {
			return Changeset{}, fmt.Errorf("changeset: match on %q: %w", f.Path, err)
		}
		if !selected {
			cs.Others = append(cs.Others, Other{Path: d.Path, Status: status})
			continue
		}
		if !o.Lean && !o.NoPatch {
			if f.Diff, err = gitrepo.PatchOf(dir, r.Base, r.Head, d); err != nil {
				return Changeset{}, err
			}
		}
		cs.Files = append(cs.Files, f)
	}
	if !o.SkipHistory {
		if err := attachCommits(dir, r, &cs); err != nil {
			return Changeset{}, err
		}
	}
	return cs, nil
}

// attachCommits says, for each selected file, which commits of the range changed
// it. A file the squashed diff shows and no commit is found to have changed is a
// fault in reading history, and an error: what grounds a file is what its commits
// carry, so a file with none must not quietly be grounded by nothing or by all.
func attachCommits(dir string, r gitrepo.Range, cs *Changeset) error {
	if len(cs.Files) == 0 {
		return nil
	}
	refs := make([]gitrepo.FileRef, len(cs.Files))
	for i, f := range cs.Files {
		refs[i] = gitrepo.FileRef{Path: f.Path, OldPath: f.OldPath}
	}
	headBlobs := make(map[string]string, len(cs.Files))
	for _, f := range cs.Files {
		headBlobs[f.Path] = f.NewBlob
	}
	hist, err := gitrepo.FileHistory(dir, r.Base, r.Head, refs, headBlobs)
	if err != nil {
		return err
	}
	for i := range cs.Files {
		p := cs.Files[i].Path
		cs.Files[i].Substantive = hist.Substantive[p]
		cs.Files[i].SameContent = hist.SameAsHead[p]
		commits := hist.Commits[p]
		if len(commits) == 0 {
			return fmt.Errorf("changeset: no commit of the range is found to have changed %q", p)
		}
		cs.Files[i].Commits = commits
	}
	return nil
}

// Selects asks a rule's `match` about one file.
//
// A rename is selected if `match` holds on its new path OR on the path it came
// from — with the markers it carried there. Otherwise moving a file out of a
// guarded path (`git mv memories/x.md archive/x.md`) would be the one way to
// change a guarded file that no rule is asked about. The second question is the
// old path, the old markers as `markers`, and the same status: the file the rule
// guards, as it was.
func Selects(sel func(Scope) (bool, error), s Scope) (bool, error) {
	ok, err := sel(s)
	if err != nil || ok || s.Status != "R" || s.OldPath == "" {
		return ok, err
	}
	old := s
	old.Path, old.Markers = s.OldPath, s.OldMarkers
	return sel(old)
}

// Admits says whether a status may enter `files` at all under a deletions mode.
// A rename is not a deletion.
func Admits(mode DeletionMode, status byte) bool {
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
func readFile(dir string, r gitrepo.Range, d gitrepo.Delta, scan Scanner, lean, raw bool) (File, error) {
	f := File{Path: d.Path, Status: string(d.Status), OldPath: d.OldPath, OldBlob: d.OldBlob, NewBlob: d.NewBlob, OldMarkers: []Marker{}, NewMarkers: []Marker{}}
	if lean {
		return f, nil
	}
	oldPath := d.Path
	if d.OldPath != "" {
		oldPath = d.OldPath
	}
	if d.Status != 'A' && !d.Gitlink {
		content, err := blobOf(dir, r.Base, oldPath, d.OldBlob, raw)
		if err != nil {
			return File{}, err
		}
		f.OldContent = content
		f.OldMarkers = markersOf(scan, content)
	}
	if d.Status != 'D' && !d.Gitlink {
		content, err := blobOf(dir, r.Head, d.Path, d.NewBlob, raw)
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

func blobOf(dir, commit, path, oid string, raw bool) (string, error) {
	if raw && oid != "" {
		return gitrepo.BlobByID(dir, oid)
	}
	return gitrepo.BlobAt(dir, commit, path)
}
