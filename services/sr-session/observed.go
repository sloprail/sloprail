package main

import (
	"path/filepath"

	"github.com/sloprail/sloprail/internal/gitrepo"
)

// treeDifference is what a cycle changed, in the shape filemod.Observed asks
// for. It is the first implementation of that interface.
//
// The split is the one the interface documents: this half owns the repository
// and establishes what differs, and filemod turns that into the event
// vocabulary. Git is this side's resource, so nothing in the module has to know
// what a commit is.
type treeDifference struct {
	root string

	// commit is the point the difference is measured from, kept so BaselineContent
	// can read a file's prior bytes out of it. The diff needs it too, but that is
	// computed in the constructor; this field is what survives for the per-path
	// content reads a Post update or delete asks for.
	commit string

	// paths in the order they will be reported, already cleaned.
	paths []string

	// baseline answers ExistedAtBaseline, keyed on the SAME cleaned spelling
	// that appears in paths.
	//
	// Keyed clean rather than on what git said, even though the two are the
	// same today. What the contract forbids is a map whose keys and whose
	// reported paths are spelled differently, and the way that happens is a
	// producer keying on its input and reporting something normalised. Cleaning
	// once, before either is built, makes the two spellings the same value by
	// construction rather than by both happening to be canonical.
	baseline map[string]bool
}

// newTreeDifference asks git what the cycle changed and holds the answer in the
// form filemod.Observed reads it in.
//
// The cleaning happens here, once, and both the reported path and the map key
// come out of the same variable. That is the whole of the guarantee: there is
// no path in this struct that was not cleaned, and no second spelling for
// ExistedAtBaseline to be keyed on.
//
// A path that cleans to something already held is dropped rather than added
// twice. Git does not emit two spellings of one path, but a rename whose source
// and destination clean to the same string would, and one entry per file is
// what the interface promises.
// The directory asked about is the one the hook was invoked in, which is NOT
// necessarily the root the answers are relative to. gitrepo.Change.Path is
// repository-relative, and Observed.Root is documented as the repository root,
// so the root is resolved rather than assumed to be the cwd.
//
// Assuming them equal is F1. A hook invoked below the top of the tree would
// hold paths relative to the root and a root that is the subdirectory, so
// joining the two resolves nothing: every modified file outside that
// subdirectory stats as absent, and a file still sitting on disk is classified
// as a DELETE.
func newTreeDifference(dir, commit string) (*treeDifference, error) {
	root, err := gitrepo.Root(dir)
	if err != nil {
		return nil, err
	}
	// The changes and the problem travel together. gitrepo reports a status it
	// could not classify without discarding the paths it could, so a nil
	// difference and a non-nil error are different answers: the first means
	// nothing could be established, the second means not everything could.
	changes, unclassified := gitrepo.Changed(dir, commit)
	if changes == nil && unclassified != nil {
		return nil, unclassified
	}
	d := differenceOf(root, changes)
	// The commit is the point BaselineContent reads a file's prior bytes from.
	// Set here rather than in differenceOf so that helper stays a pure function of
	// (root, changes) — the shape its own comment relies on for checking the
	// cleaning against spellings git does not produce.
	d.commit = commit
	return d, unclassified
}

// differenceOf builds the difference from changes already established.
//
// Separated from the git call so the cleaning can be checked against spellings
// git does not produce. Git's own paths are already canonical, which makes the
// cleaning below a no-op on every real input — and a guarantee that only holds
// because its input happens to be clean is not a guarantee, it is a
// coincidence that a future producer of these Changes would silently break.
func differenceOf(root string, changes []gitrepo.Change) *treeDifference {
	d := &treeDifference{
		root:     root,
		baseline: make(map[string]bool, len(changes)),
	}
	for _, c := range changes {
		// One variable, used for BOTH the reported path and the map key. This
		// is the whole of the guarantee: there is no way to key the map on a
		// spelling other than the one Paths reports, because there is only one
		// spelling in scope.
		clean := filepath.Clean(c.Path)
		if _, seen := d.baseline[clean]; seen {
			continue
		}
		d.baseline[clean] = c.ExistedAtBaseline
		d.paths = append(d.paths, clean)
	}
	return d
}

// include adds a path the difference did not already hold, so a file that is no
// longer a difference is still put in front of the rules.
//
// This is what `refusal_outlives_baseline` needs and what the diff alone cannot
// supply. A refused file drops out of the difference the moment the measuring
// point moves past it — a branch switch onto a line that already holds the same
// content, or the agent committing its own unfixed work — and from then on the
// tree says nothing about it while the file is still broken on disk.
//
// Reported as an EXISTING file rather than a creation. It is not this cycle's
// work: it was there at the point being measured from, which is precisely why
// the diff is silent about it, and calling it a create would tell every rule
// bound to creation about a file the cycle did not create.
//
// A path already in the difference is left exactly as the diff classified it.
// The diff's answer is the better one wherever it has one — it knows whether the
// file was at the baseline, and a refusal knows only that the file was refused
// once — so re-adding would at best duplicate the event and at worst relabel a
// genuine create as an update.
func (d *treeDifference) include(path string) {
	clean := filepath.Clean(path)
	if _, seen := d.baseline[clean]; seen {
		return
	}
	d.baseline[clean] = true
	d.paths = append(d.paths, clean)
}

// Root implements filemod.Observed.
func (d *treeDifference) Root() string { return d.root }

// Paths implements filemod.Observed.
func (d *treeDifference) Paths() []string { return d.paths }

// ExistedAtBaseline implements filemod.Observed.
//
// Asked with the canonical spelling, which is the only spelling this producer
// ever built a key from. A path this was not asked about answers false, which
// is the honest answer for a lookup that finds nothing rather than a claim: the
// interface only asks about paths that came from Paths, and every one of those
// is in the map.
func (d *treeDifference) ExistedAtBaseline(path string) bool {
	return d.baseline[path]
}

// BaselineContent implements filemod.Observed.
//
// The file's bytes at the commit the difference is measured from, read straight
// out of git — this side's resource, which is the whole reason the method lives
// on the producer rather than in the module. Asked with the canonical spelling,
// the same one Paths reports and ExistedAtBaseline is keyed on, because that is
// what git resolves against the commit's tree.
//
// Read from the repository ROOT rather than the hook's cwd: the path is
// repository-relative, so git must be asked from the top of the tree for the
// `<commit>:<path>` form to resolve. A read that fails — the path was not in the
// commit, which is the ordinary case for a create — comes back false, and the
// module carries "" honestly rather than dropping the event.
func (d *treeDifference) BaselineContent(path string) (string, bool) {
	return gitrepo.ContentAt(d.root, d.commit, path)
}

// empty reports whether the cycle changed nothing that needs judging.
//
// Worth asking before the difference is turned into events: a cycle that
// touched no files still ends, and Stop still fires, but there is no reason
// to run an extractor over an empty list.
func (d *treeDifference) empty() bool { return len(d.paths) == 0 }
