package filemod

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sloprail/sloprail/internal/commandmod"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
)

// Extract implements module.Module.
//
// Before an action, this reads what the harness is about to do: a write tool
// names its path outright. After a cycle it does not predict — it compares the
// tree against where the session started and reports what is actually
// different, which is what catches everything the prediction missed.
//
// This returns events ALONGSIDE a non-nil error, which module.Module's own
// documentation requires a caller not to discard, and which the caller at
// services/sr-session/session_pre_tool.go does discard: it prints the error and
// `continue`s past the events. That is precisely the silence the module is
// built to prevent — a producer degrading with ninety-nine good classifications
// dropped for one bad path — and it is unpinned in BOTH directions, since
// mutating the caller to HONOR the contract also leaves the suite green.
//
// Fixing it is another agent's, but what would pin it is worth stating, because
// an unpinned contract is how this arrives back here a fourth time. A test in
// the caller's own package, over a stub module returning one event and one
// error together, asserting that the event reaches the matching stage. It has
// to assert the EVENT's arrival and not the error's printing: the error is
// already visible on stderr, so a test watching only that passes under both
// behaviours, which is exactly why the mutation survives now. The module side
// cannot host that test — from in here the return value is correct either way,
// and what happens to it afterwards is not observable.
func (m *Module) Extract(in module.Input) ([]event.Event, error) {
	if in[module.InputPhase] == module.PhasePost {
		return m.extractObserved(in)
	}
	return m.extractPending(in)
}

// pendingWrite is the shape a write tool's arguments take. Read only to find
// the path; what else a harness puts there is its business.
type pendingWrite struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

// pendingCommand is the shape a shell tool's arguments take.
//
// Declared here rather than imported from commandmod for the reason commandmod
// gives for declaring its own Pending: two modules reading one payload is not
// two modules sharing a type. What IS imported is the parsing, which is the part
// that would otherwise be written twice and disagree.
type pendingCommand struct {
	Command string `json:"command"`
}

// Pending is what a module is given about an action a harness is about to
// take. A module reads what it recognises and ignores the rest.
type Pending interface {
	// Tool is what the harness calls it. Read only to know how to read the
	// arguments — a rule never sees it, and nothing in this module branches on
	// it. That is deliberate; extractPending's doc comment argues it.
	Tool() string
	// Arguments are the tool's own, as the harness gave them.
	Arguments() json.RawMessage
	// Root is the workspace an absolute path is reported relative to, or "" if
	// the producer does not know one.
	//
	// Observed has carried a Root since it existed, because a tree diff has to
	// say what its paths are relative to. Pending did not, and the omission was
	// not visible from in here: this module received a path and reported it,
	// and both spellings look equally like a path.
	//
	// It is visible from the matcher. A rule is written `path startsWith
	// "memories/"` — the only spelling an author can write, since they do not
	// know where the repo will be checked out — and Claude Code sends
	// `file_path` absolute. So the pre phase reported a spelling no project
	// matcher admits, and every Pre-kind rule about a path was inert.
	Root() string
}

// extractPending reads a pending action for the files it would touch.
//
// It dispatches on the SHAPE of the arguments and never on Tool(). That
// omission looks like a bug and has been reported as one, so the argument for
// it lives here, where someone about to "fix" it will read it.
//
// The objection is fair on its face: any tool whose arguments carry a
// `file_path` produces a file event, so `Read` — which is read-only — yields a
// PreFileCreate. That is real. It is measured, not hypothetical, and
// TestExtractPending_ReadOnlyToolStillProducesAnEvent pins it.
//
// It is still the right trade, for two reasons that a name allowlist cannot
// give back.
//
// The first is drift. A name allowlist is a list of strings that must track a
// vocabulary this engine does not own and is not told about. Claude Code
// renamed `Task` to `Agent` in v2.1.63; a corpus survey of 8,458 transcripts
// found filemod never noticed, because it never asked. Every module that DID
// ask broke silently — and silently is the point: an allowlist that has fallen
// behind does not error, it stops producing events, and a guardrail that stops
// firing looks exactly like a guardrail that is satisfied. That is the precise
// failure this engine exists to prevent, and it is worse than the false
// positive above, because a spurious event is visible to whoever reads the rule
// and a missing one is visible to no one.
//
// The second is that the shape already does most of the filtering, which is
// easy to miss because the defect report overstated the blast radius. Of the
// read-only tools named as producing bogus events, only `Read` actually does.
// `Grep` carries `pattern`/`path`, `Glob` carries `pattern`, `WebFetch`
// carries `url` — none carries `file_path`, so none reaches an event at all.
// The residue is `Read`, and one over-reported tool is a smaller wrong than a
// vocabulary that goes stale without saying so.
//
// commandmod makes the identical choice for the identical reason, so this is
// the engine's rule rather than this module's habit.
//
// What would change this: `Read`'s event is not merely spurious, it is
// mislabelled — a PreFileCreate for a file that already exists on disk and is
// only being read. If that becomes a problem worth solving, solve it on shape
// too. A create whose arguments carry no `content` key at all is not a write,
// and that is a question about the arguments rather than about the name.
// Distinguishing an absent `content` from an empty one needs the raw JSON
// rather than the decoded struct, which is why it is not done here today.
func (m *Module) extractPending(in module.Input) ([]event.Event, error) {
	pending, ok := in[module.InputPayload].(Pending)
	if !ok {
		return nil, nil
	}

	var w pendingWrite
	if err := json.Unmarshal(pending.Arguments(), &w); err != nil || w.FilePath == "" {
		// No path named outright. A command line may still name one, and that
		// is the other shape this module reads — see extractCommand.
		return m.extractCommand(pending)
	}

	// What the RULE sees, and what the FILESYSTEM is asked about, are two
	// different spellings of one path, and they are separated here.
	//
	// A matcher reads the reported one, so it must be the project's own
	// spelling — relative to the workspace — because that is the only spelling
	// an author can write down. The stat below must use the harness's, because
	// a relative path resolves against this process's working directory, which
	// is not the workspace.
	//
	// Conflating them is what the bug was: one absolute path went to both, the
	// stat was right and the matcher never matched.
	f := FileEvent{Path: reportable(w.FilePath, pending.Root())}
	var kind string
	// The lookup takes the path as the harness named it. Both arguments are
	// that one path: the pre phase asks about the file the tool is about to
	// write, and the tool named it in a spelling that resolves.
	p, _ := lookAt(w.FilePath, w.FilePath)
	switch p {
	case absent, unknown:
		// A stat that cannot answer is treated as "not there" HERE and nowhere
		// else, and the asymmetry with extractObserved is the point. The two
		// outcomes here are PreFileCreate and PreFileUpdate over a path the tool
		// named either way — no event appears or disappears on the choice, so the
		// lesser wrong is the one that still carries `content`, which a rule can
		// read when the file on disk is unreadable. In the observed phase the same
		// lookup alone decides whether a DELETION is announced, so there the
		// unknown is refused rather than folded.
		kind = KindPreCreate
		// The file does not exist yet, so a rule that wants to look at what
		// would be written has nowhere else to look.
		f.Content = w.Content
		f.Markers = Scan(w.Content)
	case presentFile:
		kind = KindPreUpdate
		f.Markers = m.markersOnDisk(w.FilePath)
	case presentNotAFile:
		// A directory, a device, a socket. No file write can land here — the
		// harness's own write will fail — so there is no file modification to
		// report and no event to emit. Emitting PreFileUpdate, as this did,
		// invented an existing file out of a stat that only said "something is
		// here", and put every PreFileUpdate rule to work on it.
		//
		// Silence is right rather than an error: a module that finds nothing it
		// owns says nothing, the same as the no-file_path case above. The write
		// still fails, and it fails as the harness's error about a real
		// filesystem condition rather than as a guardrail verdict.
		//
		// lookAt's error is deliberately dropped on this path. It names a producer
		// mistake, and there is no producer here — a harness aiming a write at a
		// directory is reporting a filesystem condition, not breaching a contract.
		return nil, nil
	}
	return []event.Event{f.Event(kind)}, nil
}

// reportable is the spelling a rule sees: relative to the workspace when the
// path is inside it, and unchanged otherwise.
//
// Three cases, and the third is the one worth stating.
//
// A path already relative is CLEANED and left relative. A harness that sends a
// workspace-relative path has already produced the spelling a matcher wants, so
// it is not re-resolved against the root — but it is put in canonical form
// first, and that is not cosmetic.
//
// A matcher is a prefix test, so an uncleaned relative path is a way round every
// narrowed rule in the project. `./secret/keys.md` and `secret/keys.md` name one
// file; returning the first verbatim means `path startsWith "secret/"` does not
// admit it, the hook is never asked, and the write lands. Measured end to end
// before this was cleaned: the guarded write went through unrefused. The same
// holds for `secret/./keys.md` and for any spelling with a redundant separator.
//
// It also gives the revalidation store one key per file. A verdict is recorded
// against the reported path, so two spellings of one file were two subjects, and
// a rule that had judged one had not judged the other.
//
// Clean does not resolve symlinks and does not touch the filesystem, which is
// what keeps this the cheap branch. It is purely lexical, so it cannot pull an
// outside path in: `../x` cleans to `../x` and stays outside. A relative path
// that climbs out of the workspace is left as it is for the same reason the
// absolute branch below leaves outside paths alone — no project-relative matcher
// should admit it.
//
// A path inside the workspace becomes relative to it, with forward slashes.
// This is what makes `path startsWith "memories/"` admit a write Claude Code
// announced as `/Users/x/repo/memories/...`, and it is the spelling
// extractObserved has always reported, so the Pre and Post kinds of one rule
// finally agree.
//
// A path OUTSIDE the workspace keeps its absolute spelling rather than being
// given a relative one. `filepath.Rel` would happily return
// `../../../etc/passwd`, and a matcher is a prefix test: a rule written for a
// folder in the project must not be handed a spelling that could climb into
// one. Leaving it absolute means no project-relative matcher admits it, which
// is the honest answer — the write is outside the rule's subject.
func reportable(path, root string) string {
	if !filepath.IsAbs(path) {
		return filepath.ToSlash(filepath.Clean(path))
	}
	if root == "" {
		// No workspace was named, so there is nothing to be relative TO.
		// Guessing one would be worse than reporting what the harness said.
		return filepath.ToSlash(path)
	}
	// resolve, not a lexical test, and this is the whole reason the check is not
	// three lines of filepath.Rel.
	//
	// A lexical answer is wrong on the case presence.go calls "the worse of the
	// two": a repo containing `escape -> /outside`, written to at
	// `<root>/escape/id_rsa`. Rel returns `escape/id_rsa` with no `..` in it, so
	// every string check passes and the event carries a CLEAN RELATIVE PATH
	// NAMING A FILE OUTSIDE THE REPOSITORY. A hook joins it against its own root
	// and reads the outside file, and nothing anywhere reports a problem.
	//
	// Measured, not argued: with the lexical version this returned
	// "escape/id_rsa" for a file in a different temp directory entirely.
	//
	// resolve is the check that already exists for this, it touches the
	// filesystem precisely because a containment test that does not cannot see
	// this case, and it also returns the ONE canonical spelling — so `a.md` and
	// `./a.md` stop producing two events for one file.
	if rel, err := filepath.Rel(root, path); err == nil {
		if clean, _, err := resolve(root, rel); err == nil {
			return filepath.ToSlash(clean)
		}
	}
	// The two may still be one directory spelled differently. filepath.Rel is
	// string arithmetic and knows nothing about symlinks, so on macOS a repo at
	// `/private/tmp/x` (as git resolves it) and a path under `/tmp/x` (as the
	// harness spells it) look like different trees and produce `../../tmp/...`.
	//
	// Resolving both and asking again is what tells the two cases apart: a
	// genuinely-outside path is still outside once resolved, so this cannot
	// pull one in. It runs only after the plain answer says "outside", which
	// keeps the filesystem out of the common path.
	// Both spellings resolved, then asked again THROUGH resolve for the same
	// reason as above: this branch must not become the lexical hole the first
	// branch stopped being. A genuinely-outside path is still outside once
	// resolved, so re-asking cannot pull one in — it only closes the gap
	// between two spellings of one directory.
	realRoot, errRoot := filepath.EvalSymlinks(root)
	realPath, errPath := filepath.EvalSymlinks(path)
	if errRoot == nil && errPath == nil {
		if rel, err := filepath.Rel(realRoot, realPath); err == nil {
			if clean, _, err := resolve(realRoot, rel); err == nil {
				return filepath.ToSlash(clean)
			}
		}
	}
	// A path that does not exist yet cannot be resolved — which is the ordinary
	// case for PreFileCreate. Resolve the deepest parent that does exist and
	// re-attach the remainder, so a create under a symlinked workspace is
	// reported the same way an update to an existing file there is.
	//
	// resolveAsFarAsItGoes is presence.go's, not a second copy: the same
	// deepest-existing-parent walk this needs already exists there for the same
	// reason, and two versions of it are two things to keep in step.
	if errRoot == nil {
		partial := resolveAsFarAsItGoes(path)
		if rel, err := filepath.Rel(realRoot, partial); err == nil {
			if clean, _, err := resolve(realRoot, rel); err == nil {
				return filepath.ToSlash(clean)
			}
		}
	}
	return filepath.ToSlash(path)
}



// extractCommand reads a pending shell command for the files it would change.
//
// # Why this is here and not in a module of its own
//
// `rm notes.md` deletes a file, so it is a PreFileDelete, so it is this
// module's: the registry refuses two modules claiming one kind
// (TestNewRegistry_TwoModulesClaimingOneKindIsRefused), and a third module
// emitting file kinds would collide with this one at registration. The only
// way a third module could exist is by declaring kinds of its own, which is the
// fourth-kind design the spec argues against — see events/main.tsp.
//
// It is also what the spec already says. fileExtract's own documentation reads
// "a write tool names its path outright, a shell command has to be parsed for
// its redirections", which puts both shapes in one command. This is that
// sentence being true.
//
// What is NOT duplicated is the parsing. Reading a command line means walking
// shell syntax, undoing quoting and unwrapping `sudo`, and commandmod already
// does all of it — so this imports commandmod.FileTargets rather than reparsing.
// Two parses of one line are two answers free to disagree, and the expensive
// half of `event_derived_once` is exactly this walk.
//
// # Why a target's kind is decided here rather than there
//
// commandmod returns paths and effects, never events, because what kind an
// event is depends on what is at the path — and reading the tree is this
// module's business. A command line cannot say whether `sed -i f.md` updates an
// existing file or fails on a missing one; only a stat can.
//
// # Why a write to a path that is absent produces nothing
//
// The three outcomes below are exhaustive and one of them is silence:
//
//	Remove, file present   PreFileDelete. The path is named, nothing else is
//	                       needed, and this is the case the whole defect was
//	                       about.
//	Write, file present    PreFileUpdate. Carries no pending content for a tool
//	                       write either, so a command's inability to name the
//	                       resulting bytes costs the rule nothing.
//	anything, absent       Nothing at all.
//
// That last row is the honest limit rather than an oversight. A write to a path
// that does not exist is a CREATION, and PreFileCreate requires `content` — the
// file cannot be read off disk, so the event carries what would be written. A
// command line does not say what bytes will result. Sending `content: ""` would
// make `echo x > new.md` indistinguishable from a tool writing a genuinely empty
// file, and `content == ""` is precisely the rule an author writes to catch
// that; omitting the field contradicts the declaration, and a matcher reading a
// declared-but-absent field errors, which refuses the action and blames the
// author's rule for this engine's gap. Both are worse than saying nothing.
//
// So a command that CREATES a file is not predicted. It is reported after the
// fact, as PostFileCreate off the tree diff, which sees the file whatever made
// it. Prevention is offered for what can be predicted honestly; the rest is
// reported.
//
// A removal of a path that is absent is likewise nothing: `rm gone.md` deletes
// no file, and announcing a deletion of something that is not there would fire
// a rule on a file that was never at risk.
//
// # Errors
//
// A stat that cannot answer is reported and produces no event for that path,
// while the other paths on the same line still do. The asymmetry with the
// tool-write path above is deliberate and matches extractObserved: there, an
// unknown decides whether a DELETION is announced, so it is refused rather than
// folded. Here it decides the same thing, so it is refused the same way. On the
// tool-write path an unknown chooses between two events over a path the tool
// named either way, which is why that one folds instead.
func (m *Module) extractCommand(pending Pending) ([]event.Event, error) {
	var pc pendingCommand
	if err := json.Unmarshal(pending.Arguments(), &pc); err != nil || pc.Command == "" {
		// A tool whose arguments carry neither a file path nor a command line
		// concerns this module not at all, which is ordinary rather than an
		// error. Note this does not gate on the tool's NAME, for the reason
		// extractPending argues at length: a harness that renames its shell tool
		// must not silently stop being watched.
		//
		// The `== ""` half is EQUIVALENT and kept anyway. An empty command line
		// parses to an empty file, which yields no targets and so no events —
		// measured for "", "   ", "\n" and "\t" alike — so removing this test
		// changes nothing observable and a mutation between the two survives.
		// It mirrors the identical guard in commandmod.Extract, and stating the
		// condition where the payload is read is what keeps the two modules
		// answering the same way about the same payload.
		return nil, nil
	}

	targets := commandmod.FileTargets(pc.Command)
	if len(targets) == 0 {
		// EQUIVALENT to falling through, and kept anyway so the survivor is read
		// as an equivalence rather than as this branch being untested. With no
		// targets the loop below never runs, both slices stay nil, and
		// errors.Join of nothing is nil — so the final return already produces
		// exactly (nil, nil). Verified by removing it: the suite stays green.
		//
		// Kept because it states the commonest outcome outright. Most commands an
		// agent runs touch no file, and a reader should not have to prove that
		// the loop and the Join below degrade correctly to nothing in order to
		// know what `git status` does here.
		return nil, nil
	}

	var events []event.Event
	var problems []error
	// One command may name a path twice — `rm a.md a.md`, or a redirection onto
	// a file the same line also touches. One file is one event, and the first
	// effect named wins: a rule should be asked once about a file, and asking
	// twice would run a judging hook twice over one decision.
	seen := make(map[string]bool, len(targets))

	for _, t := range targets {
		if seen[t.Path] {
			continue
		}
		seen[t.Path] = true

		// The pre phase has one spelling and no root, the same as the tool-write
		// path above: the command names the path as it names it, and both
		// arguments are that one path.
		p, err := lookAt(t.Path, t.Path)
		switch {
		case err != nil && p == unknown:
			// The machine could not answer. Said, and no event built on it —
			// classifying here would report a difference from a lookup that never
			// happened.
			problems = append(problems, err)
			continue
		case p != presentFile:
			// Absent, or a directory or device. Neither is a file this module's
			// kinds can honestly be about: absent is the creation case argued
			// above, and a non-file is what presentNotAFile means on the
			// tool-write path too. lookAt's error is dropped for the same reason
			// it is dropped there — it names a producer mistake, and a command
			// line aimed at a directory is not a producer.
			continue
		}

		f := FileEvent{Path: t.Path}
		kind := KindPreUpdate
		if t.Effect == commandmod.Remove {
			kind = KindPreDelete
		} else {
			// An update carries the markers the file has NOW, the same as the
			// tool-write path. See markersOnDisk on what those actually describe.
			f.Markers = m.markersOnDisk(t.Path)
		}
		events = append(events, f.Event(kind))
	}

	return events, errors.Join(problems...)
}

// markersOnDisk reads a file and scans it.
//
// IMPORTANT — what an update's markers actually describe. PreFileUpdate does not
// carry the pending content: module.go declares `path` alone for that kind, and
// the event has no field holding what the write would leave behind. So these are
// the markers in the bytes the write is about to REPLACE, not the bytes it would
// leave. On an update, `markers` describes the PRE-WRITE state of the file.
//
// That is a real limitation, not a design choice: a rule saying "this function
// must stay marked" would read the marker that is there now and be satisfied by
// a write that removes it. It is the honest reading available today, and it
// stops being a limitation when PreFileUpdate carries its pending content — a
// known defect elsewhere, deliberately not fixed here.
//
// A file that cannot be read yields no markers rather than an error. The write
// is what this event is reporting; a rule that cannot see the old text should
// still see the path, and failing the whole extraction would drop the event
// entirely — a file event that never fires is the silence this engine exists to
// prevent.
func (*Module) markersOnDisk(path string) []Marker {
	b, err := os.ReadFile(path)
	if err != nil {
		return []Marker{}
	}
	return Scan(string(b))
}

// extractObserved turns the difference between the tree and the session's
// baseline into one event per file.
//
// One event per file rather than one carrying a list: a rule about files is
// written against a file, and a cycle that touched a hundred of them should
// dispatch a hundred events each matcher narrows, not hand every hook a
// hundred-entry array to filter itself.
//
// Each path is classified from two facts and no prediction — whether it was
// there at the baseline, which the difference's producer holds, and whether it
// is there now, which is a stat. A tool call's claim about what it did reaches
// none of it.
//
// A payload this module cannot read yields nothing, which is the same answer
// extractPending gives and for the same reason: a module reads what it
// recognises.
//
// Everything else a path can be wrong about is reported rather than skipped.
// The events go out alongside the error: one path the machine could not stat,
// or one the producer named in error, is not a reason to withhold the
// classification of the ninety-nine beside it. What must not happen is the
// silence — a path dropped without a word is how a producer that always answers
// false, making every delete vanish, keeps looking like a working one.
//
// The work is split across two passes over the paths, for the reason
// baselinesFor gives: a spelling breach is a property of the whole list, and an
// event already appended cannot be taken back.
func (m *Module) extractObserved(in module.Input) ([]event.Event, error) {
	observed, ok := in[module.InputPayload].(Observed)
	if !ok {
		return nil, nil
	}

	root := observed.Root()
	paths := observed.Paths()

	events := make([]event.Event, 0, len(paths))
	var problems []error
	// Two paths that clean to the same file are one file, and Observed
	// promises everything it names becomes an event with no second filter
	// downstream — so the deduplication is this code's, and it is keyed on the
	// canonical spelling rather than on what was given.
	seen := make(map[string]bool, len(paths))

	// The baseline is established for every path BEFORE any of them is
	// classified, because the spelling breach is a property of the whole list
	// rather than of one entry, and an event emitted mid-list cannot be recalled.
	//
	// Run inside the classification loop, the check was defeated by the dedupe:
	// given {"dir/a.md", "./dir/a.md"} with the baseline keyed raw, the clean
	// spelling was classified and its event appended on the first iteration, and
	// the raw one — the only spelling that can produce the disagreement — arrived
	// afterwards, when the create had already shipped. Reversing the two inputs
	// caught it, so the guard was order-dependent in the same way as the defect
	// its own comment says it eliminates: the same file in the same tree coming
	// out a create or an update by iteration order alone.
	//
	// Two passes is what makes the answer independent of the order Paths listed
	// the spellings in. The canonical question is still asked once per FILE — the
	// contract puts one question about one file — while each distinct SPELLING is
	// probed, which is the cost the contract already prices in.
	settled, breached := m.baselinesFor(observed, root, paths, &problems)

	for _, path := range paths {
		// The first pass's own answer, not a second one. resolve is asked once
		// per spelling and the verdict is carried forward, because asking twice
		// makes the two passes able to DISAGREE — and every way they can disagree
		// is a silence or a misclassification.
		//
		// Resolved then refused: the tree moved between the calls, which the
		// producer's own ExistedAtBaseline is enough to do. Pass two refused the
		// path and `continue`d on the strength of "already reported by the first
		// pass", which had reported nothing, so a delete that was owed produced
		// neither an event nor an error — indistinguishable from a tree that did
		// not change, which is the one outcome this module exists to prevent.
		//
		// Refused then resolved: pass one never wrote the baseline, so reading
		// the map gave Go's zero value — false for a file that WAS at the
		// baseline, classifying a delete or an update as PostFileCreate. The
		// two-value read is what tells "asked, answered false" from "never
		// asked", and carrying the decision keeps the distinction instead of
		// discarding it at the boundary.
		// The `asked` half is EQUIVALENT today and kept anyway. baselinesFor
		// writes an entry for every element of `paths`, on both of its branches,
		// and this loop walks that same slice — so the lookup always hits and a
		// mutation dropping the two-value read survives the suite. It is recorded
		// here rather than left to be rediscovered as an untested guard.
		//
		// Kept because it is the distinction that makes the map's zero value
		// safe, and "always present" is a claim about the two functions agreeing
		// on one slice rather than about this read. Should a later pass ever
		// filter `paths` before the second walk, the missing entry would come
		// back as settledPath{} — resolved false, before false — and a delete
		// would classify as a create with nothing saying so.
		d, asked := settled[path]
		if !asked || !d.resolved {
			// Refused, and said so exactly once — in the first pass, which is
			// where every spelling is resolved. Reporting it again here would
			// make one bad path complain once per spelling of it.
			continue
		}
		clean, full := d.clean, d.full
		// Asked with the canonical spelling, the same one the dedupe keys on and
		// the same one the event carries — established by the first pass, and read
		// back here. Asking with the raw spelling made the answer depend on the
		// order Paths happened to list them in: given {"a.md", "./a.md"} and a
		// baseline map holding only "a.md", whichever came first won the slot, so
		// one file in one tree classified as an update or a create depending on
		// nothing but iteration order. Two spellings of one file cannot be allowed
		// to carry two baselines when the rest of the contract says spelling is
		// not part of it.
		before := d.before
		if breached[clean] {
			// A spelling of this file disagreed with the baseline in the first
			// pass. Which of the two answers is the true one is exactly what is
			// in doubt, so no event is emitted for it at all.
			continue
		}

		if seen[clean] {
			continue
		}
		// Marked before the classification rather than after it, so that a
		// repeated path is looked at once whatever it turns out to be — a file
		// named twice should not be stat'd twice, nor reported twice as a
		// producer error. Both `continue`s below are problem reports, so marking
		// after either of them makes one file complain once per spelling: three
		// spellings of one directory produce three ErrPathIsNotAFile, and the
		// count is the only thing that tells the mistake from the fix.
		seen[clean] = true

		p, err := lookAt(clean, full)
		if err != nil {
			// Either the stat could not answer — not a fact about the tree, and
			// classifying on it would report a difference from a lookup that
			// never happened — or a non-file now sits there, which exists and
			// is not what these kinds are about. Both are said and neither
			// becomes an event.
			problems = append(problems, err)
			continue
		}

		// Reached only for absent or presentFile: every other state left an error
		// above. So the boolean this narrows to is the whole remaining question,
		// and it is narrowed HERE rather than inside classify, which is about the
		// baseline/tree pair and has no business knowing what a stat can fail to
		// say.
		//
		// Which makes `p == presentFile` and `p != absent` EQUIVALENT on every
		// input that reaches this line, and a mutation between them survives the
		// suite. Recorded so the survivor is read as an equivalence rather than
		// as this branch being untested. The spelling here is the one that stays
		// correct if the guard above is ever relaxed: `!= absent` would then read
		// presentNotAFile and unknown as "the file is there" — a directory
		// classified PostFileUpdate, and an unreadable stat classified as
		// whatever the baseline happened to say. Naming the ONE state a file
		// event may be built on is the direction that cannot rot.
		kind, reportable := classify(before, p == presentFile)
		if !reportable {
			// The no/no row. Legitimately a file created and removed inside one
			// cycle, which leaves nothing to be about — and also the one place
			// the tree can contradict the producer's claim, so it is said.
			problems = append(problems, fmt.Errorf("%w: %q", ErrNotADifference, clean))
			continue
		}

		// No content on any of them, including the create. Unlike PreFileCreate
		// the file is on disk by now and a hook can read it there; and for the
		// delete there is nothing left to read at all. Carrying content on one
		// kind and not the others would make the delete the odd case a hook has
		// to special-case, which is exactly the shape the Post kinds are
		// declared flat to avoid.
		events = append(events, FileEvent{Path: clean}.Event(kind))
	}

	err := errors.Join(problems...)
	if len(events) == 0 {
		// Distinct from the empty slice on purpose: a cycle that changed
		// nothing needing judgement produced no events, and a caller appending
		// to its own slice should not have to tell an empty result from a
		// present-but-empty one.
		return nil, err
	}
	return events, err
}

// checkBaselineSpelling enforces the one spelling rule this module states and
// could not otherwise enforce.
//
// A producer keying its baseline on its own raw paths answers about
// "./dir/./a.md" and is asked about "dir/a.md" — false for a file that WAS
// there, so the update ships as a create and every delete disappears, with
// nothing on the tree contradicting any of it.
//
// The test is deliberately one-directional. A CONFORMING producer's map is
// keyed clean, so the raw spelling misses it and answers false while the
// canonical one answers true — disagreement, and entirely correct. Only the
// reverse is diagnostic: the raw spelling found something the canonical one did
// not, which no map keyed the way the contract asks can produce. That is a map
// keyed raw, said by the producer itself rather than inferred.
//
// The canonical answer is passed in rather than asked for again: it is the one
// the classification uses, and asking twice would make the module put two
// questions to the producer about one file where the contract has exactly one.
func (*Module) checkBaselineSpelling(observed Observed, path, clean string, before bool) error {
	if path == clean || before || !observed.ExistedAtBaseline(path) {
		return nil
	}
	return fmt.Errorf("%w: %q and %q", ErrBaselineKeyedOnRawSpelling, path, clean)
}

// settledPath is the first pass's verdict on one SPELLING, carried to the second
// rather than recomputed there.
//
// It holds the resolution as well as the baseline because those are the two
// facts the second pass would otherwise establish for itself, and every way the
// two passes can disagree about them is a defect the tree can cause on its own.
// A `resolved` flag rather than a nil check on clean: an unresolved path has no
// canonical spelling to be absent, and the flag says which question was answered
// instead of leaving it to be inferred from a zero value — the same distinction
// the two-value map read below is for.
type settledPath struct {
	// resolved is false when resolve refused this spelling. The refusal is
	// already in problems; nothing further is owed for it.
	resolved bool
	// clean and full are resolve's own answers, meaningful only when resolved.
	clean, full string
	// before is what the baseline said about clean. Meaningful only when
	// resolved, because that is the only case in which it was asked.
	before bool
}

// baselinesFor asks the baseline about every path, once per file, and returns
// what it settled about each SPELLING along with the files a breach was found on.
//
// It is the first of the two passes, and its whole purpose is to finish before
// any event exists. Resolution errors are reported here, so the classification
// pass simply skips what it cannot resolve rather than reporting it twice.
//
// The verdicts are keyed on the RAW spelling, which is what the second pass
// iterates: two spellings of one file each get an entry, and both point at the
// one canonical answer, so the baseline is still asked once per file. Keyed on
// the canonical spelling instead there would be no entry at all for a spelling
// that was refused, and "refused" would be indistinguishable from "never seen".
//
// A path repeated verbatim overwrites its own entry with the identical verdict,
// since resolve is a function of (root, path) and the baseline is memoised per
// file. Nothing is lost and nothing accumulates.
func (m *Module) baselinesFor(observed Observed, root string, paths []string, problems *[]error) (settled map[string]settledPath, breached map[string]bool) {
	settled = make(map[string]settledPath, len(paths))
	breached = make(map[string]bool, len(paths))
	// The canonical answer, memoised so the contract's one-question-per-file
	// promise survives however many spellings named it.
	baselines := make(map[string]bool, len(paths))

	for _, path := range paths {
		if _, done := settled[path]; done {
			// This exact spelling has already been resolved and its verdict
			// recorded. Resolving it again would ask the filesystem a second
			// time about a question already answered, and a tree that moved in
			// between would answer differently — the same disagreement between
			// two resolutions that the second pass no longer makes. It is also
			// what keeps a refusal to ONE complaint however many times the
			// producer repeated the bad spelling.
			continue
		}
		clean, full, err := resolve(root, path)
		if err != nil {
			*problems = append(*problems, err)
			settled[path] = settledPath{}
			continue
		}
		before, asked := baselines[clean]
		if !asked {
			before = observed.ExistedAtBaseline(clean)
			baselines[clean] = before
		}
		settled[path] = settledPath{resolved: true, clean: clean, full: full, before: before}
		if breached[clean] {
			// Reported once per file, not once per spelling of it.
			continue
		}
		if err := m.checkBaselineSpelling(observed, path, clean, before); err != nil {
			*problems = append(*problems, err)
			breached[clean] = true
		}
	}
	return settled, breached
}
