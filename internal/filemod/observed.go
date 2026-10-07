package filemod

// Observed is what this module is given at the end of a cycle: the paths the
// cycle touched, and for each of them the one fact about the baseline that
// cannot be recovered by looking at the tree now.
//
// An interface rather than a struct for the same reason Pending is one — the
// thing that produces it lives in the service, which imports this package, so a
// concrete type here would have to be constructed there anyway and a method set
// is the smaller contract.
//
// Why the paths are given rather than found: comparing the working tree against
// the session's baseline means reading git, and git is not this module's
// resource. The half that owns the repository establishes what differs; this
// half turns that into the event vocabulary. Splitting it there is also what
// keeps `untouched_stays_silent` honest — a module that walked the tree itself
// would have to re-derive which of what it found was this session's work.
type Observed interface {
	// Root is the directory the paths below are relative to — the repository
	// root. Needed because the events carry repository-relative paths by
	// contract, and a relative path cannot be stat'd without knowing what it is
	// relative to. Resolving it against the process's working directory instead
	// would make the classification depend on where the hook happened to be
	// invoked from.
	//
	// An empty root is refused rather than defaulted. Joining against "" is
	// exactly the resolution against the process's working directory this
	// method exists to prevent, and it would prevent it everywhere except where
	// the producer forgot.
	Root() string

	// Paths are the repository-relative paths that differ from the baseline,
	// committed work and outstanding work alike. A path that has not changed
	// since the baseline must not appear: everything here becomes an event, and
	// there is no second filter downstream.
	//
	// What "repository-relative" excludes is enforced, not assumed: absolute
	// paths, paths escaping through "..", the root itself, and paths that reach
	// outside through a symlinked parent directory are reported as
	// ErrPathNotRelativeToRoot rather than normalised into something stattable.
	// The last of those needs the filesystem to see — "escape/id_rsa", where
	// "escape" links to a directory outside, passes every string check and
	// joins cleanly into a readable outside file — so containment is checked
	// against the resolved tree, not against the spelling.
	// Spelling is not part of the contract — "a.md" and "./a.md" are the same
	// file and yield one event carrying the canonical form, so a duplicate here
	// is tolerated rather than doubled.
	Paths() []string

	// ExistedAtBaseline reports whether the path was present at the point the
	// cycle's difference is measured from.
	//
	// It is asked with the CANONICAL spelling — filepath.Clean of what Paths
	// gave, the same form the event carries — not with the raw spelling. A
	// producer that keys a map on its own paths must clean them first, and one
	// whose paths are already clean, which is what git reports, need do nothing.
	//
	// That requirement is checked rather than trusted. Where a path's raw and
	// canonical spellings differ, both are asked, and a producer answering two
	// ways about one file is reported as ErrBaselineKeyedOnRawSpelling instead
	// of having one of its two answers picked for it. Unchecked it was the one
	// contract term whose breach produced no error anywhere: the baseline comes
	// back false for a file that was there, the update ships as a create, and
	// the tree agrees with every event.
	//
	// Asked with the raw spelling instead, this method would be the one place
	// spelling mattered, and the rest of the contract says it does not: "a.md"
	// and "./a.md" are one file and yield one event. Two spellings of one file
	// reaching a baseline map that answers differently for each made the
	// classification depend on which Paths listed first — the same file in the
	// same tree coming out a create or an update by iteration order alone. One
	// file has one baseline, so there is one spelling to ask about it with.
	//
	// This is the half of the classification the tree cannot answer. Whether a
	// file is there now is a stat away; whether it was there before is a fact
	// about a commit that has since been left behind, and only the producer of
	// the difference still holds it.
	//
	// Which is also why a wrong answer here is invisible: always-false makes
	// every delete disappear and every update look like a create, always-true
	// the reverse, and nothing in the tree contradicts either. Nothing except
	// the no/no row — a path never at baseline and not on disk now differs from
	// nothing, which Paths says cannot happen. That one case is reported as
	// ErrNotADifference so a producer degrading this way says so somewhere.
	ExistedAtBaseline(path string) bool

	// BaselineContent is the file's bytes at the point the cycle's difference is
	// measured from, and whether they could be read. Asked with the CANONICAL
	// spelling, the same form Paths reports and the event carries.
	//
	// This is the other half of the classification the tree cannot answer, and
	// the reason it lives on the producer rather than in this module. What a file
	// holds NOW is a stat and a read away — this module does that itself, off
	// disk. What it held BEFORE the change is a fact about a commit the tree may
	// since have left behind, and reading it means reading git — which is the
	// producer's resource, not this module's. So the Post update and delete
	// events, which carry `oldContent`, get it from here.
	//
	// The boolean is the honest half. A file that was not at the baseline (a
	// create) has no prior bytes, and a producer that cannot read the blob it
	// once had answers false rather than "" — because "" is a legitimate prior
	// content (a file that was empty at the baseline), and a rule about what was
	// lost must tell the two apart. A create is never asked, since it declares no
	// oldContent; an update or delete whose baseline read fails carries "" and is
	// reported honestly rather than dropped, the same way an unreadable file NOW
	// yields no markers rather than failing the whole extraction.
	BaselineContent(path string) (string, bool)
}

// classify decides which Post kind a changed path is, from the two facts that
// settle it: whether the path was there at the baseline, and whether it is
// there now.
//
//	baseline  now  →  kind
//	no        yes  →  PostFileCreate
//	yes       yes  →  PostFileUpdate
//	yes       no   →  PostFileDelete
//	no        no   →  nothing
//
// The last row is a path that was created and removed within the same cycle.
// It leaves no difference against the baseline and nothing on disk to judge, so
// there is no file for a rule to be about. Reporting it as a delete would ask
// every rule bound to deletion about a file the project never had.
//
// That is one of two ways to reach the row. The other is a producer that
// answered ExistedAtBaseline wrongly, and the tree cannot tell them apart. Both
// yield no event; extractObserved reports the row so the second does not pass
// as the first in silence.
//
// Neither fact is inferred from what a tool call said. A write tool that
// reported creating a file it in fact overwrote, and a shell command nothing
// parsed, both land in the same two lookups.
// sr:invariant events/post-changes-are-the-tree-diff
func classify(existedBefore, existsNow bool) (string, bool) {
	switch {
	case !existedBefore && existsNow:
		return KindPostCreate, true
	case existedBefore && existsNow:
		return KindPostUpdate, true
	case existedBefore && !existsNow:
		return KindPostDelete, true
	default:
		return "", false
	}
}
