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
	// paths, paths escaping through "..", and the root itself are reported as
	// ErrPathNotRelativeToRoot rather than normalised into something stattable.
	// Spelling is not part of the contract — "a.md" and "./a.md" are the same
	// file and yield one event carrying the canonical form, so a duplicate here
	// is tolerated rather than doubled.
	Paths() []string

	// ExistedAtBaseline reports whether the path was present at the point the
	// cycle's difference is measured from. It is asked with the path exactly as
	// Paths gave it, so a producer may key it on its own spelling.
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
