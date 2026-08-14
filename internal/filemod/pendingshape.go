package filemod

import (
	"encoding/json"
	"errors"
	"strings"
)

// This file reads a pending tool call for the bytes it would leave behind.
//
// # The defect it exists to fix
//
// extractPending decoded `tool_input` into a struct holding `file_path` and
// `content` and read `.content`. `Edit` carries `old_string`/`new_string` and
// no `content` at all, so Content decoded to "" and an Edit that CREATES a file
// was announced as an empty one. Measured through `sr-session pre-tool`:
//
//	Write new.md   {"content":"FULL NEW BODY"}                      content="FULL NEW BODY"  correct
//	Edit exists.md {"old_string":"line one","new_string":"..."}     PreFileUpdate            correct
//	Edit other.md  {"old_string":"","new_string":"CREATED BY EDIT"} content=""               WRONG
//	MultiEdit fresh.md {"edits":[{"old_string":"","new_string":"MADE BY MULTIEDIT"}]}
//	                                                                content=""               WRONG
//	NotebookEdit nb.ipynb {"notebook_path":...,"new_source":...}    no event at all          WRONG
//
// The last is the worst of them: `notebook_path` is not `file_path`, so the
// module saw no path, handed off to extractCommand, found no command either,
// and produced nothing. Every Pre rule was blind to notebook writes — and the
// shell hook this engine replaced (`reflect-on-edits.sh`) matched NotebookEdit
// explicitly, so the migration lost coverage the project already had.
//
// # Absent is not empty, and this is why the raw JSON is read
//
// extractPending's closing note said distinguishing an absent `content` from an
// empty one "needs the raw JSON rather than the decoded struct, which is why it
// is not done here today". That is exactly right and it is what this file does:
// every field is a *string or json.RawMessage, so "the key was not there" and
// "the key held an empty string" are different values rather than both being "".
//
// The distinction is the whole defect. `content == ""` is the rule an author
// writes to catch a genuinely empty file, and until now it fired on every
// Edit-created file whatever the body.
//
// # Shape, never the tool's name
//
// Nothing here reads Tool(). Claude Code renamed `Task` to `Agent` in v2.1.63
// and every module asking by name broke silently — see extractPending's doc
// comment for the full argument. A payload carrying `old_string`/`new_string`
// is an edit whatever a vendor calls it next, and one carrying `notebook_path`
// names a file just as surely as one carrying `file_path`.
//
// This also means a tool nobody has invented yet is read correctly the day it
// ships, provided it spells its arguments the way its siblings do — which is
// the property a name allowlist cannot offer at all.

// pendingArgs is a pending tool call's arguments, decoded so that an ABSENT key
// is distinguishable from a present-but-empty one.
//
// Every field that can be empty is a pointer or a RawMessage for that reason.
// A plain string would collapse the two cases, which is the defect.
type pendingArgs struct {
	// FilePath is the path a write tool names.
	FilePath string `json:"file_path"`

	// NotebookPath is what a notebook tool names its path. A second key for the
	// same concept rather than a second code path: see pendingArgs.path.
	NotebookPath string `json:"notebook_path"`

	// Content is the whole resulting body, stated outright. A pointer because
	// `{"content":""}` is a genuinely empty file and `{}` is a tool that states
	// no body at all, and those must not look alike.
	Content *string `json:"content"`

	// OldString and NewString are one replacement. Pointers for the same
	// reason: an empty `old_string` is the CREATE spelling — it means "match
	// nothing, insert this" — and is entirely different from the key's absence.
	OldString *string `json:"old_string"`
	NewString *string `json:"new_string"`

	// ReplaceAll makes a repeated old_string unambiguous instead of an error.
	ReplaceAll bool `json:"replace_all"`

	// Edits is MultiEdit's array. RawMessage rather than a decoded slice so
	// that a malformed array is distinguishable from an absent one, and so an
	// empty array — which changes nothing — is distinguishable from both.
	Edits json.RawMessage `json:"edits"`

	// NewSource is one notebook CELL's source. Deliberately not treated as file
	// content; see resultFor on why a notebook yields a path-only event.
	NewSource *string `json:"new_source"`

	// EditMode is a notebook edit's kind — replace, insert, delete. Read only
	// to know that a delete is not a create.
	EditMode string `json:"edit_mode"`
}

// path is the file this call names, under either spelling.
//
// `file_path` and `notebook_path` are the same concept with two names, and
// accepting both here is what keeps the rest of the module free of a notebook
// branch. Reading a second KEY is not the same as reading a tool NAME: the key
// is the argument's own shape, so a harness that renames `NotebookEdit` keeps
// working, which is the property the name allowlist could not give.
//
// file_path wins when both are present. Nothing sends both today; if something
// ever does, the generic spelling is the one every other tool means by it.
func (a pendingArgs) path() string {
	if a.FilePath != "" {
		return a.FilePath
	}
	return a.NotebookPath
}

// isNotebook reports whether this call is aimed at a notebook, decided by which
// key carried the path rather than by the tool's name.
func (a pendingArgs) isNotebook() bool {
	return a.FilePath == "" && a.NotebookPath != ""
}

// edit is one replacement within a call.
type edit struct {
	Old *string `json:"old_string"`
	New *string `json:"new_string"`
}

// errNotDerivable means the resulting bytes cannot be worked out. It is not a
// producer error and is never reported to a caller as one: it selects between
// emitting a result and emitting none.
var errNotDerivable = errors.New("filemod: the resulting content is not derivable")

// errWillNotApply means the action itself is about to FAIL, so no file
// modification is going to happen and no event should be emitted at all.
//
// Distinct from errNotDerivable, and the distinction decides whether an event
// exists. "I cannot compute the result" still leaves a real write to report;
// "this edit cannot be applied" means there is nothing to report, because the
// tool will refuse it and the bytes will not change.
//
// This is Q2's answer. The alternative — emitting a create or an update anyway
// — is a FALSE EVENT: a rule fires on work that is not going to happen, a
// judging hook spends a model call deciding about nothing, and a refusal blames
// the agent for an action the harness was never going to take. The tree diff at
// session stop still reports whatever actually lands, so nothing is lost by
// staying quiet.
var errWillNotApply = errors.New("filemod: the pending edit cannot be applied")

// resultFor works out the bytes a pending call would leave in the file.
//
// `before` is what the file holds now, and `exists` says whether it is there at
// all — the caller has already stat'd it, and passing the answer in keeps this
// function from reading the tree a second time and disagreeing.
//
// The tiers, most-certain first, and each is decided on the SHAPE of the
// arguments:
//
//	content stated       the whole body, given outright. Nothing to compute.
//	edits[] present      apply them in order; each sees the last one's result.
//	old/new present      one edit, the same machinery with a single element.
//	notebook             not derivable — see below.
//	none of these        not derivable. `Read` lands here, and so does any tool
//	                     that names a path without saying what it would write.
//
// # Why a notebook is not derivable
//
// `new_source` is one CELL's source, and the file is a JSON document holding a
// cell array with outputs, metadata and execution counts. The resulting FILE
// bytes are not that string.
//
// They could in principle be computed — read the .ipynb, find the cell by
// `cell_id`, replace its source, re-serialise. That is rejected deliberately.
// Re-serialising means this engine deciding the document's key order,
// indentation and unicode escaping, and a notebook rewritten by a guardrail
// preview would differ from what Jupyter actually writes. A `result` that is
// close but not equal is worse than none: a rule comparing it against what
// lands would disagree for reasons that have nothing to do with the rule.
//
// So a notebook gets a path-only event — the kind, the path, and
// `resultKnown: false`. That is strictly better than today's silence, which is
// no event at all, and it is honest about what is not known. What it is NOT is
// `content: ""`, which is the wrong this whole change removes.
func resultFor(a pendingArgs, before string, exists bool) (string, error) {
	switch {
	case a.Content != nil:
		// The tool stated the whole body. This outranks a replacement even when
		// both are present, because a stated body is strictly more information
		// than an instruction for producing one — and a real payload does carry
		// both: TestExtract_ExtraArgumentKeysAreIgnored sends a Write-shaped
		// call with a stray `old_string`.
		return *a.Content, nil

	case len(a.Edits) > 0:
		return applyEditsJSON(a.Edits, before, exists)

	case a.OldString != nil && a.NewString != nil:
		return applyEdits([]edit{{Old: a.OldString, New: a.NewString}}, before, exists, a.ReplaceAll)

	case a.isNotebook():
		// A notebook write is real and its path is known; its resulting bytes
		// are not. Both facts are reported.
		//
		// EQUIVALENT to falling through to the default, and kept anyway so the
		// survivor is read as an equivalence rather than as this branch being
		// untested. Both arms return errNotDerivable, so a mutation deleting
		// this case leaves the suite green — verified.
		//
		// Kept because the two are the same only by arithmetic. A notebook is
		// not-derivable for a REASON the default has nothing to do with: the
		// default means "this call states no body at all" (a `Read`), while
		// this means "a body was stated and it is one cell of a JSON document
		// the engine declines to re-serialise". Those are different facts that
		// happen to share an answer, and naming the notebook case here is what
		// stops the next person from reading `new_source` as content — the
		// tempting wrong fix that
		// TestExtractPending_ANotebookNeverReportsCellSourceAsFileContent
		// exists to forbid.
		return "", errNotDerivable

	default:
		// A path named with no statement of what would be written. `Read` is
		// the commonest, and extractPending's doc comment accepts that it still
		// produces an event — the drift-immunity trade argued there.
		return "", errNotDerivable
	}
}

// applyEditsJSON decodes an edits array and applies it.
//
// A malformed array yields errNotDerivable rather than errWillNotApply: the
// engine could not read the instruction, which says nothing about whether the
// tool can. Guessing that the call will fail would suppress an event for a
// write that may well happen.
//
// An EMPTY array is different and yields errWillNotApply. It is well-formed and
// says outright that nothing is to be changed, so there is no modification to
// report.
func applyEditsJSON(raw json.RawMessage, before string, exists bool) (string, error) {
	var edits []edit
	if err := json.Unmarshal(raw, &edits); err != nil {
		return "", errNotDerivable
	}
	if len(edits) == 0 {
		return "", errWillNotApply
	}
	// replace_all is per-call on Edit and per-edit on MultiEdit. The per-edit
	// spelling is not decoded here because an edits array that repeats a string
	// is refused below anyway, which is the conservative direction: a false
	// "cannot apply" costs a missed event that the tree diff still catches,
	// while a false "applied" reports bytes the file never holds.
	return applyEdits(edits, before, exists, false)
}

// applyEdits applies a sequence of replacements in order, each seeing the
// previous one's result.
//
// # Q3, and why the order is not an implementation detail
//
// The edits are sequential: a later one matches against what the earlier ones
// produced, not against the original bytes. Applying them all to `before` would
// be wrong whenever one edit's output is another's input — the ordinary case
// for a refactor that renames a symbol and then changes its call site.
//
// The accumulator below IS that ordering. It is tested by a fixture in which
// the second edit's old_string exists only after the first has run, so an
// implementation that reset to `before` each time produces nothing at all
// rather than a subtly different answer.
//
// # Atomicity
//
// The tool applies the whole array or none of it. So one edit that cannot apply
// fails the CALL, and the partial result of the edits that did apply is a state
// the file never reaches. Reporting it would be a lie about the outcome, which
// is why the error propagates instead of the loop breaking.
func applyEdits(edits []edit, before string, exists bool, replaceAll bool) (string, error) {
	if len(edits) == 0 {
		return "", errWillNotApply
	}

	cur := before
	created := !exists
	for i, e := range edits {
		if e.Old == nil || e.New == nil {
			// Half a replacement is not an instruction this can carry out, and
			// it is not evidence the tool will fail either — a harness may
			// spell an insertion some way this does not know.
			return "", errNotDerivable
		}
		old, new := *e.Old, *e.New

		if old == "" {
			// The create spelling: match nothing, insert this.
			//
			// Legitimate only as the FIRST edit against a file that is not
			// there. Once the file exists — on disk, or because an earlier edit
			// in this same call created it — an empty old_string has no defined
			// insertion point, and the tool refuses it rather than guessing
			// between prepending and appending.
			if i == 0 && created {
				cur = new
				continue
			}
			return "", errWillNotApply
		}

		if created && i == 0 {
			// A non-empty old_string against a file that does not exist. There
			// is nothing for it to match, so the tool fails. This is the
			// create-side of Q2.
			//
			// EQUIVALENT today and kept anyway, so the survivor is read as an
			// equivalence rather than as this branch being untested. On a create
			// `cur` is "", so strings.Count below returns 0 for any non-empty
			// `old` and the `n == 0` arm returns the identical errWillNotApply
			// two lines on. Verified by mutation: replacing this condition with
			// `false` leaves the whole suite green.
			//
			// Kept because the two are the same only while `cur` starts empty on
			// a create. It states the requirement where the CREATE case is
			// decided — there is no file for a replacement to match — rather than
			// leaving it to be re-derived from the counting arm, which is about
			// something else. It is also the spelling that stays correct if a
			// later change ever seeds `cur` from somewhere other than "".
			return "", errWillNotApply
		}

		n := strings.Count(cur, old)
		switch {
		case n == 0:
			// Not found. The tool refuses the edit, so the write does not
			// happen — Q2's central case.
			return "", errWillNotApply
		case n > 1 && !replaceAll:
			// Ambiguous. The tool refuses rather than picking one, and the
			// engine must not pick either: reporting the first occurrence's
			// result would be bytes the file never holds.
			//
			// With replace_all the same input is fully determined, which is why
			// that flag is threaded here rather than treated as advisory.
			return "", errWillNotApply
		case replaceAll:
			cur = strings.ReplaceAll(cur, old, new)
		default:
			cur = strings.Replace(cur, old, new, 1)
		}
	}
	return cur, nil
}
