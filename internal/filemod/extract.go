package filemod

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
// documentation requires a caller not to discard. BOTH callers now honour that:
// services/sr-session/session_pre_tool.go and dispatch_post.go each append the
// events first and report the error after.
//
// This comment used to say the Pre caller discarded them and that the contract
// was "unpinned in BOTH directions", and it outlived both facts. The discard is
// gone, and the test it asked for exists: TestPreTool_ModuleErrorDoesNotDiscard-
// ItsEvents, in the caller's own package, over a module returning one event and
// one error together. It asserts the EVENT's arrival rather than the error's
// printing, which is the distinction the old note was right about — a test
// watching stderr alone passes under either behaviour. Verified by mutation:
// restoring the `continue` turns that test red.
//
// Why it mattered, kept because it is the argument for not regressing it:
// `rm a.md b.md` is ONE tool call producing TWO targets, so one path that will
// not stat makes this return a problem for that path together with a perfectly
// good PreFileDelete for the other. Dropping the slice meant the rule guarding
// the readable file never ran and the deletion proceeded, with the only trace on
// a stream that at exit 0 reaches no agent.
func (m *Module) Extract(in module.Input) ([]event.Event, error) {
	if in[module.InputPhase] == module.PhasePost {
		return m.extractObserved(in)
	}
	return m.extractPending(in)
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
// It dispatches on the HARNESS TOOL NAME first — commandmod.HarnessWriteTools
// and commandmod.HarnessCommandTools — and only reads argument shape for a
// tool that name already says is write-capable. This is a reversal from how
// this function used to work, and the reversal is a deliberate project
// decision, not a discovery that the old reasoning was wrong. See
// commandmod/harnesstools.go for the full argument and the trade accepted:
// a hand-maintained allowlist, with no shape fallback for an unlisted tool.
//
// # What this replaced
//
// This USED to dispatch on argument SHAPE and never on Tool() at all — any
// tool whose arguments carried `file_path` produced a file event regardless
// of the tool's name, on the reasoning that a name allowlist tracks a
// vocabulary this engine does not own (Claude Code renamed `Task` to `Agent`
// in v2.1.63 with no warning, and a corpus survey found every module that
// asked by name broke silently). That version of this function is preserved
// in git history rather than restated here; the short version is that shape
// dispatch initially treated ANY path-naming call as a write — so `Read`
// yielded a `PreFileCreate` — until a PREVENTIVE file-guard bound to those
// kinds was measured refusing the READ itself (a guard failing closed on an
// unverifiable result cannot tell "a real write with unknown bytes" apart
// from "no write at all"; `Read` never carries `content`, so it looked like
// the former). That was fixed by asking the ARGUMENTS whether they stated a
// write at all (pendingArgs.statesAWrite, since removed — see pendingshape.go
// for where it was and why asking the tool name now makes asking the
// arguments the same question a second time).
//
// # Why the tool-name gate this time, when the argument above still holds
//
// The drift risk the shape-based design was built to avoid is real and is
// not being denied here — a write tool renamed and not added to
// HarnessWriteTools now produces nothing, silently, until the list catches
// up. This project chose to accept that cost anyway, deliberately: the
// people operating this engine are the same people who choose which harness
// tools it watches, and maintaining a short, named list on a rename is
// judged the smaller and more honest cost against inferring "is this a
// write" from argument shape for every call, forever — which is what let a
// plain `Read` synthesize a write event in the first place. A tool-name gate
// makes a missed rename visible (a guardrail an author expects to fire does
// not), where shape inference made the Read misclassification invisible
// until a guard happened to fail closed on it.
//
// What is UNCHANGED beneath the gate: resultFor's own tier list (content,
// edits, old_string/new_string, notebook) still decides what bytes a
// recognised write tool's call would leave behind, and its errNotDerivable
// default still exists for a write whose bytes cannot be worked out (a
// notebook edit). The gate decides WHETHER a call is a write; resultFor still
// decides WHAT it writes.
func (m *Module) extractPending(in module.Input) ([]event.Event, error) {
	pending, ok := in[module.InputPayload].(Pending)
	if !ok {
		return nil, nil
	}

	// ONE call has ONE tool name, so these two are mutually exclusive by
	// construction — see harnesstools.go: HarnessWriteTools and
	// HarnessCommandTools are disjoint vocabularies (Write-shaped tools vs.
	// the shell tool), and nothing here assumes otherwise.
	tool := pending.Tool()

	if commandmod.HarnessCommandTools[tool] {
		// A recognised shell tool. Its arguments are a command line, read by
		// extractCommand below — never by the write-tool branch, because a
		// command tool's arguments do not carry `file_path`/`content` in the
		// first place.
		return m.extractCommand(pending)
	}

	if !commandmod.HarnessWriteTools[tool] {
		// Not a tool this project has named as write-capable or
		// command-capable — see commandmod.HarnessWriteTools for the trade
		// accepted: the tool-name gate is the SOLE signal, with no shape
		// fallback, so an unrecognised tool produces no event however its
		// arguments happen to be shaped. This is what excludes Read (never
		// on either list) without asking anything about its arguments at
		// all, and it is also what a renamed write or shell tool now costs:
		// nothing, until the relevant list is updated.
		return nil, nil
	}

	var w pendingArgs
	if err := json.Unmarshal(pending.Arguments(), &w); err != nil || w.path() == "" {
		// A recognised write tool whose arguments do not parse, or name no
		// path. Its own shape says nothing further can be built.
		return nil, nil
	}
	path := w.path()

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
	f := FileEvent{Path: reportable(path, pending.Root())}
	var kind string
	// The lookup takes the path as the harness named it. Both arguments are
	// that one path: the pre phase asks about the file the tool is about to
	// write, and the tool named it in a spelling that resolves.
	p, _ := lookAt(path, path)

	// What the file holds now, read ONCE and threaded into the derivation.
	//
	// An edit's result is a function of the current bytes, so this is needed
	// wherever a replacement has to be applied. Reading it here rather than
	// inside resultFor keeps the tree read on this one path: resultFor is a
	// pure function of what it is given, which is what makes it testable by
	// handing it two strings — the same division commandmod draws between
	// what the line says and what the filesystem answers.
	//
	// Only for a file that is actually there. `before` is "" for a create,
	// which is what an edit inserting into a new file must see.
	before := ""
	if p == presentFile {
		before = m.contentOnDisk(path)
	}
	result, derr := resultFor(w, before, p == presentFile)
	if errors.Is(derr, errWillNotApply) {
		// The tool is about to refuse this edit, so no bytes change and there
		// is no file modification to be about. Q2: emitting a create or an
		// update here would be a false event — a rule firing, and possibly a
		// judging hook spending a model call, on work that is not going to
		// happen.
		//
		// Silence rather than an error, the same answer the presentNotAFile
		// branch below gives: the tool's own failure is a real condition it
		// will report itself, not a guardrail verdict and not a producer
		// breach.
		return nil, nil
	}
	derivable := derr == nil

	switch p {
	case absent, unknown:
		// A stat that cannot answer is treated as "not there" HERE and nowhere
		// else, and the asymmetry with extractObserved is the point. The two
		// outcomes here are PreFileCreate and PreFileUpdate over a path the tool
		// named either way — no event appears or disappears on the choice, so the
		// lesser wrong is the one that still carries `newContent`, which a rule can
		// read when the file on disk is unreadable. In the observed phase the same
		// lookup alone decides whether a DELETION is announced, so there the
		// unknown is refused rather than folded.
		kind = KindPreCreate
		// The file does not exist yet, so a rule that wants to look at what
		// would be written has nowhere else to look.
		//
		// `result` is what the action would leave behind, which for a create is
		// the whole body — so it IS newContent, and PreFileCreate declares that
		// one name for it. Where it could not be derived this is "", which is
		// the pre-existing behaviour for a path named without a stated body
		// (`Read`) and is pinned as such.
		//
		// resultKnown is the boolean that tells that underivable "" apart from
		// a genuinely-empty create, exactly as on an update. A notebook create
		// is the case that matters: it names a real file with a real pending
		// write whose bytes are not derivable, so it reaches here with
		// derivable=false and newContent="", and a preventive file-guard reads
		// resultKnown=false to fail closed rather than judging the empty string
		// as the file. A stated empty body (`Write content:""`) is
		// derivable=true, so the same "" is a KNOWN empty file the guard may
		// legitimately judge. newMarkers are scanned only when the result is
		// real — an underivable "" has none.
		f.NewContent = result
		f.ResultKnown = derivable
		if derivable {
			f.NewMarkers = Scan(result)
		}
	case presentFile:
		kind = KindPreUpdate
		// The bytes before and after. oldContent is the file on disk; newContent
		// is the post-edit bytes, and resultKnown says whether they could be
		// worked out — the value alone cannot, because Matcher.env fills a
		// declared-but-absent field with its zero value, so an underivable result
		// would read as "" and look exactly like a write that empties the file.
		// The boolean is what makes the gap askable.
		f.OldContent = before
		f.NewContent = result
		f.ResultKnown = derivable
		// oldMarkers describe the bytes being REPLACED — the file as it stands
		// now — and newMarkers describe the result. newMarkers is empty when the
		// result could not be derived (there is no text to scan), which resultKnown
		// tells apart from a result that genuinely carries no markers.
		f.OldMarkers = Scan(before)
		if derivable {
			f.NewMarkers = Scan(result)
		}
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
		clean := filepath.ToSlash(filepath.Clean(path))
		if root == "" {
			// Nothing to check containment against. The lexical answer is the
			// whole answer, as it was before a root was ever consulted here.
			return clean
		}
		// Cleaned is NOT the same as contained, and this is the half the
		// lexical branch was missing.
		//
		// Clean is pure string arithmetic, so it settles `./a.md` and
		// `secret/./keys.md` — the redundant spellings this branch exists to
		// canonicalise — and it settles a `..` that is VISIBLE in the spelling,
		// which stays `../x` and is left outside. What it cannot see is a `..`
		// that is not spelled at all: `escape/id_rsa`, where `escape` is a
		// symlink to a directory outside the repository, contains no `..`, is
		// already clean, and names a file the project does not hold.
		//
		// That is the identical case the ABSOLUTE branch below stopped being
		// lexical in order to catch, and the spelling of the input is no reason
		// for the two to disagree: one harness announces the write as
		// `<root>/escape/id_rsa` and another as `escape/id_rsa`, and only the
		// first was refused. Measured before this: the relative spelling was
		// reported as `escape/id_rsa`, a CLEAN REPOSITORY-RELATIVE PATH NAMING
		// AN OUTSIDE FILE — so a hook joining it against its own root reads
		// whatever the link points at, which is what resolve exists to prevent.
		//
		// resolve is the same check, reached the same way, so the two branches
		// cannot drift. It touches the filesystem, which the comment above
		// rightly calls the expensive part — but only to the depth the absolute
		// branch already pays for, and only where a root was named.
		if c, _, err := resolve(root, clean); err == nil {
			return filepath.ToSlash(c)
		}
		// Not contained, or not a path resolve can answer about.
		//
		// Reported ABSOLUTE, which is the same answer the absolute branch gives
		// an outside path and for the same reason: a matcher is a prefix test,
		// and `escape/id_rsa` is a spelling `path startsWith "escape/"` admits.
		// Handing back the cleaned relative form would leave the rule judging an
		// outside file as though the project held it — the hole itself. An
		// absolute spelling is admitted by no project-relative matcher, which is
		// the honest answer: the write is outside the rule's subject.
		//
		// Anchored against the root rather than the process's working
		// directory. This function is reached with the workspace in hand
		// precisely so the answer does not depend on where the hook fired, and
		// filepath.Abs would reintroduce that dependence.
		//
		// A path that climbs out in its own spelling — `../x` — already carries
		// its own evidence of being outside and keeps it. So does `.`, which
		// resolve refuses for naming the root rather than a file in it: both are
		// already unadmitted by any project-relative matcher, and rewriting
		// either would change a spelling that was never the hole.
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return clean
		}
		return filepath.ToSlash(filepath.Join(root, clean))
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
	// The rule then goes silently inert on a spelling nobody chose — measured
	// against real guardrails, which permitted a write they had just refused
	// when the same repo was named by its resolved path.
	//
	// Resolving both and asking again is what tells the two cases apart, and the
	// re-ask goes THROUGH resolve for the same reason the first branch does:
	// this must not become the lexical hole that branch stopped being. A
	// genuinely-outside path is still outside once resolved, so re-asking cannot
	// pull one in — it only closes the gap between two spellings of one
	// directory. It runs only after the plain answer says "outside", which keeps
	// the filesystem off the common path.
	//
	// The PATH is resolved with resolveAsFarAsItGoes rather than EvalSymlinks,
	// and that single choice is what lets one branch serve both cases. A path
	// that does not exist yet cannot be resolved at all — the ordinary
	// PreFileCreate case — so EvalSymlinks fails on it and an existence-gated
	// branch would skip a create under a symlinked workspace, reporting it
	// differently from an update to a file already there. resolveAsFarAsItGoes
	// resolves the deepest parent that DOES exist and re-attaches the remainder,
	// which is the same answer for a path that exists (nothing is missing, so
	// the remainder is empty and it equals EvalSymlinks exactly) and the right
	// one for a path that does not.
	//
	// This was two branches, an EvalSymlinks-both one ahead of this one, and the
	// first was dead: it could only answer when EvalSymlinks(path) succeeded,
	// and in exactly that case resolveAsFarAsItGoes returns the identical
	// string, so every answer it gave this one gives too. Deleting it changed no
	// test — which is the honest reason it is gone rather than any claim that
	// one branch is tidier. Recorded so it is not helpfully restored.
	//
	// resolveAsFarAsItGoes is presence.go's, not a second copy: the same
	// deepest-existing-parent walk this needs already exists there for the same
	// reason, and two versions of it are two things to keep in step.
	if realRoot, err := filepath.EvalSymlinks(root); err == nil {
		if rel, err := filepath.Rel(realRoot, resolveAsFarAsItGoes(path)); err == nil {
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
// that does not exist is a CREATION, and PreFileCreate requires `newContent` —
// the file cannot be read off disk, so the event carries what would be written.
// A command line does not say what bytes will result. Sending `newContent: ""`
// would make `echo x > new.md` indistinguishable from a tool writing a genuinely
// empty file, and `newContent == ""` is precisely the rule an author writes to
// catch that; omitting the field contradicts the declaration, and a matcher
// reading a declared-but-absent field errors, which refuses the action and blames the
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
	// The tool-name gate is the CALLER's, not this function's: extractPending
	// only reaches here for a tool in commandmod.HarnessCommandTools. This
	// function stayed nameless-by-design for a while (see git history on this
	// comment) on the reasoning that a harness renaming its shell tool must
	// not silently stop being watched — the same reasoning extractPending's
	// own doc comment used to make about itself. Both now gate on name, by
	// the same project decision; see commandmod/harnesstools.go for the
	// argument and the cost accepted.
	var pc pendingCommand
	if err := json.Unmarshal(pending.Arguments(), &pc); err != nil || pc.Command == "" {
		// A recognised command tool whose arguments carry no command line —
		// ordinary, not an error.
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

	// A copy whose destination turns out to be a DIRECTORY writes several files
	// inside it rather than one file at it, and only a stat can tell. Resolved
	// before the loop so each resulting file is classified by exactly the same
	// code every other target is — see expandIntoDirectories.
	targets = expandIntoDirectories(targets)

	var events []event.Event
	var problems []error
	// One command may name a path twice — `rm a.md a.md`, or a redirection onto
	// a file the same line also touches. One file is one event, and the first
	// effect named wins: a rule should be asked once about a file, and asking
	// twice would run a judging hook twice over one decision.
	seen := make(map[string]bool, len(targets))

	for _, t := range targets {
		// Keyed on the CANONICAL spelling, not the raw one, because "one file"
		// is a fact about the file and not about how the line spelled it.
		//
		// This was keyed on t.Path, and the sibling loop in extractObserved
		// keys on `clean` — the same dedup, one canonical and one not, which is
		// the half-fix shape. Two spellings of one path therefore survived as
		// two targets and then collapsed into two IDENTICAL reported events.
		// Measured before this:
		//
		//	printf x > notes.md; printf y > ./notes.md      -> 2x PreFileCreate "notes.md"
		//	printf x > notes.md; printf y > <root>/notes.md -> 2x PreFileCreate "notes.md"
		//	touch notes.md; touch ./notes.md                -> 2x PreFileCreate "notes.md"
		//
		// The cost is exactly what the paragraph above forbids: a judging hook
		// — a model call — runs twice over one decision, and an author reading
		// the report sees one file refused twice for the same reason. It is
		// invisible in the ordinary case because the identical spelling twice,
		// which is what the test pinned, was already caught by the raw key.
		//
		// reportable() is the canonical spelling and is what the event carries,
		// so it is the honest key. The RAW spelling is still what the filesystem
		// is asked with below — that separation is the point, and this only
		// decides which target is looked at, never how.
		key := reportable(t.Path, pending.Root())
		if seen[key] {
			continue
		}
		seen[key] = true

		// The two spellings are separated here exactly as extractPending
		// separates them, and for the same reason: the FILESYSTEM is asked with
		// the path the command named, because that is the spelling that
		// resolves; the RULE is shown reportable(), because a matcher is a
		// prefix test and the only spelling an author can write is the
		// workspace-relative one.
		//
		// This path used to report t.Path raw, and that was a hole rather than
		// an inconsistency. A command naming an absolute path inside the
		// workspace — which is what an agent writes whenever it has the repo
		// root in hand — produced an event whose `path` was absolute, so
		// `path startsWith "memories/"` did not admit it and every Pre-kind rule
		// narrowed on a project folder was bypassed by respelling one argument.
		// Measured before the fix: `printf x > memories/topics/t/A.md` was
		// refused and `printf x > <root>/memories/topics/t/B.md` was permitted,
		// the same write either way.
		p, err := lookAt(t.Path, t.Path)
		switch {
		case err != nil && p == unknown:
			// The machine could not answer. Said, and no event built on it —
			// classifying here would report a difference from a lookup that never
			// happened.
			problems = append(problems, err)
			continue
		case p == absent && t.Effect == commandmod.Write:
			// A creation. Historically silent, and the doc comment above gives
			// the reason: PreFileCreate requires `content`, the file cannot be
			// read off disk, and a command line "does not say what bytes will
			// result".
			//
			// That is true of most command lines and FALSE of a real subset,
			// which is what changed. `echo hi > new.md`, `touch new.md`, a
			// quoted heredoc and `cp a.md new.md` all determine the resulting
			// bytes exactly, and commandmod now says so through Payload. Where
			// it does, the create is predicted carrying the content the file
			// will actually have.
			//
			// Where it does NOT — `some-unknown-tool > new.md` — the original
			// argument stands unchanged and the creation stays silent, because
			// `content: ""` would make it indistinguishable from `touch`, whose
			// empty content is a fact. The tree diff reports it afterwards.
			content, ok := m.resolvePayload(t.Payload, "")
			if !ok {
				continue
			}
			events = append(events, FileEvent{
				Path:       key,
				NewContent: content,
				NewMarkers: Scan(content),
			}.Event(KindPreCreate))
			continue
		case p != presentFile:
			// A directory or device, or an absent path being removed. Neither is
			// a file this module's kinds can honestly be about: a non-file is
			// what presentNotAFile means on the tool-write path too, and `rm
			// gone.md` deletes nothing. lookAt's error is dropped for the same
			// reason it is dropped there — it names a producer mistake, and a
			// command line aimed at a directory is not a producer.
			continue
		}

		f := FileEvent{Path: key}
		kind := KindPreUpdate
		if t.Effect == commandmod.Remove {
			kind = KindPreDelete
			// A delete carries the bytes about to be lost and the markers that go
			// with them — the same oldContent/oldMarkers the tool-write delete
			// path would carry, read off the file the command names.
			before := m.contentOnDisk(t.Path)
			f.OldContent = before
			f.OldMarkers = Scan(before)
		} else {
			// An update carries oldContent — the file NOW — and its oldMarkers,
			// the same as the tool-write path.
			before := m.contentOnDisk(t.Path)
			f.OldContent = before
			f.OldMarkers = Scan(before)
			// And, where the line determines them, the bytes it will hold
			// afterwards. `echo x >> log.md` is the append case: the result is
			// the current file plus the new text, which needs the disk for the
			// base and the line for the tail, so neither package could answer
			// it alone.
			//
			// mtimeOnly is the guard that keeps a create-shaped payload from
			// lying about an existing file, and it is the whole reason this
			// decision lives HERE rather than in commandmod. `touch` determines
			// an empty file when it CREATES one and changes nothing at all when
			// the file is already there — one line, two outcomes, chosen by the
			// tree. commandmod cannot make that choice without reading the
			// filesystem, so it states the create case and this branch corrects
			// it. Applying the payload blindly would report `touch existing.md`
			// as emptying the file, and a rule refusing empty results would
			// fire on a command that changes no byte.
			if t.MTimeOnly {
				f.NewContent, f.ResultKnown = before, true
			} else {
				f.NewContent, f.ResultKnown = m.resolvePayload(t.Payload, before)
			}
			// newMarkers describe the result, present only when it was derivable.
			if f.ResultKnown {
				f.NewMarkers = Scan(f.NewContent)
			}
		}
		events = append(events, f.Event(kind))
	}

	return events, errors.Join(problems...)
}

// expandIntoDirectories resolves the one ambiguity a copy's last operand
// carries: whether it is a file being written or a directory being written
// INTO.
//
// `cp a.md b.md target/` produces target/a.md and target/b.md, not a file
// called target. commandmod states both readings — Path with its Payload for
// the file case, FileTarget.Into for the directory case — precisely because
// choosing between them means a stat, and this is the side that stats. See
// FileTarget.Into for the argument.
//
// The rule for the resulting name is base(source), which is cp's and mv's own
// and is not a tree question. What IS a tree question is whether it applies,
// and a target whose path is not a directory right now passes through
// untouched, keeping whatever payload it already had.
//
// Each expanded target carries a copy reference to its OWN source, so
// target/a.md gets a.md's bytes and target/b.md gets b.md's. A single payload
// spread across both would attach one file's contents to the other's path,
// which is the confidently-wrong class of answer rather than a missing one.
//
// The DIRECTORY itself stops being reported, and dropping it is EQUIVALENT
// today — measured, not assumed. Kept in this shape anyway, so the survivor is
// read as an equivalence rather than as an untested line: a directory reaches
// the loop below as presentNotAFile and is dropped by the `p != presentFile`
// branch, so keeping it here changes nothing observable. Verified by keeping
// it: the suite stays green.
//
// It is dropped HERE regardless, because the two are the same only by the
// coincidence that a copy destination is always a real directory when Into is
// non-empty. What the drop states is that the directory is not one of the paths
// that CHANGE — which is why it must not sit in the caller's `seen` map. Should
// a later reading ever expand a target whose own path is also written (a shape
// `cp` does not have but a future entry could), the surviving directory would
// claim that path's slot in `seen` and suppress its real event.
//
// A source whose basename is `.` or `..` — which no ordinary copy has, but a
// crafted line could — is skipped rather than joined. filepath.Join collapses
// them: `target/` + Base("sub/.") is `target` itself, and + Base("sub/..") is
// the path ABOVE it. Joining either would predict a target for a path the copy
// does not create, one of them outside the destination entirely.
//
// EQUIVALENT today — measured, not assumed. Both collapsed paths are
// DIRECTORIES, so the loop below drops them at its `p != presentFile` branch
// before any event is built, and removing this guard leaves the suite green.
// TestExtractCommand_ACopySourceWithNoBasenameNamesNoFileInside pins the
// OUTCOME rather than this line, which is the assertion that stays right
// whichever way the collapse is prevented.
//
// Kept because the equivalence rests entirely on the collapsed path happening
// to be a directory, which is true only because `.` and `..` name one. It says
// nothing about a future source spelling, and nothing about a caller that
// classifies before the drop. Refusing to BUILD a path the copy does not create
// is the statement that cannot rot; relying on a later branch to discard it is
// a coincidence of two independent decisions.
func expandIntoDirectories(targets []commandmod.FileTarget) []commandmod.FileTarget {
	var out []commandmod.FileTarget
	for _, t := range targets {
		if len(t.Into) == 0 {
			out = append(out, t)
			continue
		}
		// isDirectory rather than lookAt, because lookAt's tri-state
		// deliberately does NOT distinguish a directory from a device or a
		// socket — they are all presentNotAFile, since none can receive a file
		// write. Here the difference is the whole question. See isDirectory,
		// which lives beside the oracle so the tree is still read in one place.
		if !isDirectory(t.Path) {
			// Not a directory, so the operand is an ordinary destination and
			// the payload commandmod already attached is the right reading.
			out = append(out, t)
			continue
		}
		for _, src := range t.Into {
			name := filepath.Base(src)
			if name == "." || name == ".." || name == string(filepath.Separator) {
				continue
			}
			out = append(out, commandmod.FileTarget{
				Path:    filepath.Join(t.Path, name),
				Effect:  t.Effect,
				Payload: commandmod.Payload{Kind: commandmod.PayloadCopyOf, From: []string{src}},
			})
		}
	}
	return out
}

// resolvePayload turns what a command LINE determined into the actual bytes,
// reading the filesystem where the line only referred to it.
//
// This is filemod's half of the split payload.go describes. commandmod stays a
// pure function of a string — it never stats and never reads, which is what
// lets every case there be tested by handing it a line — and the resolutions
// that need the tree happen here, where a cwd exists and files are already
// being read.
//
//	PayloadLiteral   the bytes are in hand. Nothing to resolve.
//	PayloadAppend    the file's current bytes plus the literal tail. `before` is
//	                 passed in rather than re-read, so the markers and the
//	                 result describe the same snapshot — re-reading would let
//	                 the two disagree if the tree moved between them.
//	PayloadCopyOf    read the source. An unreadable or missing source yields
//	                 "not known" rather than "", because an empty string would
//	                 claim `cp missing.md b.md` produces an empty file when in
//	                 fact the command fails and produces nothing.
//	PayloadNone      the line determined nothing.
//
// The boolean is the honest half. It becomes `resultKnown` on an update, and on
// a create it decides whether the event is emitted at all — PreFileCreate's
// `content` is required, so a create that cannot state its bytes is exactly the
// case the original argument leaves to the tree diff.
func (m *Module) resolvePayload(p commandmod.Payload, before string) (string, bool) {
	switch p.Kind {
	case commandmod.PayloadLiteral:
		return p.Text, true
	case commandmod.PayloadAppend:
		return before + p.Text, true
	case commandmod.PayloadCopyOf:
		// The source as the command line spelled it, which is the same spelling
		// the target's path uses — both come off the line, and neither has been
		// resolved against a root.
		//
		// The two checks below are MUTUALLY REDUNDANT and JOINTLY load-bearing,
		// which is worth stating because each survives mutation alone and the
		// pair does not. A missing source fails lookAt and would also fail
		// ReadFile; a directory fails lookAt and would read as an error too. So
		// removing either one leaves the suite green, and removing BOTH turns
		// TestExtractCommand_CopyingFromAnUnreadableSourceClaimsNothing and
		// TestExtractCommand_ACreationIsNotPredictedWhenItsBytesAreUnknowable
		// red — measured, not asserted.
		//
		// Both are kept because they answer different questions. lookAt asks
		// "is this a regular file this module's kinds can be about", which is
		// the same tri-state question every other path in this module asks and
		// is what rejects a directory for the right reason. The error check
		// asks "did the read actually work", which covers a permission denial
		// and a file that vanished between the two calls. Dropping either would
		// leave the survivor accidentally carrying a case it does not describe.
		//
		// SEVERAL sources, concatenated in the order the line names them. A
		// copy is the one-element case: `cp a.md b.md` and
		// `cat a.md > b.md` resolve through the same code, so an unreadable
		// source cannot mean one thing for a copy and another for a
		// concatenation.
		//
		// ANY unreadable source fails the whole payload rather than being
		// skipped. `cat a.md missing.md > c.md` writes a.md's bytes and then
		// FAILS, so c.md's final contents are not what a partial concatenation
		// would report — and reporting the readable prefix would be the
		// confidently-wrong answer this module exists to avoid.
		var b strings.Builder
		for _, from := range p.From {
			if src, err := lookAt(from, from); err != nil || src != presentFile {
				return "", false
			}
			text, err := os.ReadFile(from)
			if err != nil {
				return "", false
			}
			b.Write(text)
		}
		if len(p.From) == 0 {
			// A copy naming no source determines nothing.
			//
			// EQUIVALENT today and kept anyway, so the survivor is read as an
			// equivalence rather than as an unreachable guard. commandmod never
			// builds a PayloadCopyOf with an empty From — copyPayload always
			// carries one, and catConcatenation refuses a source list that came
			// out empty — so the loop above cannot fall through with nothing
			// written. Verified by removing it: the suite stays green.
			//
			// Kept because it names the requirement at the point where the
			// bytes are DECIDED, rather than leaving it as a property of two
			// constructors in another package. The failure it would guard is
			// exactly the collision this whole tier exists to prevent: a
			// zero-source copy resolving to ("", true) is `content: ""` standing
			// in for "not known", indistinguishable from `touch`.
			return "", false
		}
		return b.String(), true
	}
	return "", false
}

// contentOnDisk reads a file, or yields "" when it cannot be read.
//
// The empty string is deliberately not distinguished from an unreadable file
// HERE, and the distinction is not lost: an unreadable file makes every
// non-empty `old_string` fail to match, so applyEdits returns errWillNotApply
// and no event claims a result at all. A file that is genuinely empty behaves
// the same way for the same reason, which is correct — an edit expecting text
// in an empty file does not apply either.
//
// The one case that reaches a result through here is an edit with an empty
// old_string, which is refused on an existing file regardless. So no derived
// `result` is ever built on bytes this failed to read.
func (*Module) contentOnDisk(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
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

		// Content and markers, per what the kind carries. Unlike the Pre events,
		// both sides have settled: newContent is the file as it now sits on disk,
		// and oldContent is the session baseline's — no longer on disk, which is
		// why it comes from the producer rather than a read here.
		//
		// A create has only newContent; a delete has only oldContent; an update
		// has both. Each is read only where the kind declares it, so a delete
		// never reads the disk (there is nothing there) and a create never asks
		// the baseline (nothing preceded it). Event() carries only the declared
		// fields regardless, but reading only what is needed keeps a delete from
		// a pointless disk read and a create from a pointless baseline lookup.
		f := FileEvent{Path: clean}
		switch kind {
		case KindPostCreate:
			f.NewContent = m.contentOnDisk(full)
			f.NewMarkers = Scan(f.NewContent)
		case KindPostDelete:
			// The baseline bytes, about to be gone. A read that fails yields "",
			// reported honestly rather than dropped — the same discipline a file
			// that cannot be read NOW gets.
			f.OldContent, _ = observed.BaselineContent(clean)
			f.OldMarkers = Scan(f.OldContent)
		case KindPostUpdate:
			f.OldContent, _ = observed.BaselineContent(clean)
			f.OldMarkers = Scan(f.OldContent)
			f.NewContent = m.contentOnDisk(full)
			f.NewMarkers = Scan(f.NewContent)
		}
		events = append(events, f.Event(kind))
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
//
// `path == clean` is what holds that for the common case — every path git
// reports is already canonical — and it is MEASURED rather than merely stated:
// TestObserved_ACanonicalPathIsAskedAboutOnce counts the calls and fails at two.
// Dropping the term survives every other test in the suite, because both calls
// return the same answer for the same string and only the COUNT changes; it was
// found by mutation testing, not by a failing test. The count matters for a
// producer whose answers are expensive, logged, or non-idempotent — today's is a
// map read, so this costs nothing and guards against a future one.
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
