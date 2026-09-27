package dispatch

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/sloprail/sloprail/internal/commandmod"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/transcript"
)

// This file is the `require` half of the check-runner: the two Prerequisite
// kinds the spec defines (dot-dir-file-store/main.tsp Prerequisite), evaluated at
// the moment a rule's check would run.
//
// The two differ not in shape but in WHO establishes them and when a violation is
// discovered — which is exactly why they are evaluated differently here:
//
//   - `{skill}` is a fact about the TRAJECTORY: was a real Skill tool_use for that
//     skill made earlier in this session. Read natively from the record, the same
//     thing the deprecated require-skill.sh established through ~100 lines of jq
//     plumbing, absorbed here so a gate declaring `require: [{skill}]` gets it for
//     free.
//   - `{context}` is an ENGINE ORDERING guarantee: the named context must have run
//     its own enter this cycle before this rule's check. For THIS slice a
//     `{context}` prerequisite reads whatever context state exists in the map the
//     caller threaded in — the full context lifecycle is the next slice, which
//     populates that map. So the check here is "is the named context active in the
//     state we were handed"; ordering is the next slice's to guarantee.

// checkRequire evaluates every prerequisite in order and refuses on the first
// that does not hold.
//
// In order, and first-failure-ends-it, so the remedy a caller sees names the
// nearest unmet precondition rather than a pile of them — the same
// one-refusal-at-a-time shape the checks use. A prerequisite that sets neither
// field is impossible in a loaded rule (the validator refuses it), so it is
// treated as vacuously satisfied here rather than guarded against a second time.
func (r Runner) checkRequire(req Request) (Verdict, error) {
	for _, p := range req.Require {
		v, err := r.checkPrerequisite(req, p)
		if err != nil {
			return Verdict{}, err
		}
		if v.Refused {
			return v, nil
		}
	}
	return pass(), nil
}

// checkPrerequisite evaluates one prerequisite.
//
// Exactly one of Skill / Context is set on a loaded rule (the validator's
// exactly-one-of check), so this dispatches on which one is present. Skill is
// tried first only because it is the field listed first in the spec; the two are
// mutually exclusive so order is immaterial.
func (r Runner) checkPrerequisite(req Request, p declaration.Prerequisite) (Verdict, error) {
	if p.Skill != "" {
		return r.checkSkill(req, p.Skill, p.Files)
	}
	if p.Context != "" {
		return r.checkContext(req, p.Context), nil
	}
	// Neither set: a loaded rule cannot reach here (the validator refuses an empty
	// prerequisite), so an empty one establishes nothing to fail and passes.
	return pass(), nil
}

// checkSkill refuses unless the session's own trajectory holds either a real
// Skill tool_use for this skill, or evidence that its own SKILL.md was read
// directly (a Read tool_use on the file, or a file-reading Bash command) — see
// skillLoadedInTrajectory for both halves — AND, for every entry in files
// (optional), evidence that THAT subpage was read too (subpageReadInTrajectory).
//
// files is checked in declared order, first-missing-ends-it, same as
// checkRequire itself does across the whole require list — one remedy at a
// time, naming the nearest unmet page, rather than a pile of them. The skill
// itself is checked first: a files entry naming a subpage of a skill that was
// never loaded at all gets the skill's own remedy, not a confusing "read
// script-checks.md" with no mention that the skill itself is missing too.
//
// "In the session's own trajectory" excludes sub-agents, which is what
// skillLoaded does — a skill loaded (or its file read) inside a delegated
// sub-agent was not loaded on the line of work doing the writing, the same
// default the deprecated require-skill.sh took by excluding sidechains.
//
// A transcript that cannot be read, or that was never named (TranscriptPath ==
// ""), is a REFUSAL, not a permit — a precondition that could not be checked is
// not a precondition that passed. This is the fail-closed rule the deprecated
// script spelled out at length, kept here: turning a gap in the environment into
// consent is the one failure a guardrail must not have.
func (r Runner) checkSkill(req Request, skill string, files []string) (Verdict, error) {
	if req.TranscriptPath == "" {
		// No record to read, so whether the skill was loaded is unknowable. Refuse
		// with the remedy, naming why — the same reasoning require-skill.sh applied
		// to an unset SR_TRANSCRIPT.
		return refuse(fmt.Sprintf(
			"this rule requires the %q skill to have been loaded first, but this session's record could not be located, "+
				"so whether it was loaded is unknown. Refusing: a precondition that could not be checked is not a precondition that passed.",
			skill)), nil
	}

	loaded, err := r.skillLoaded(req.TranscriptPath, req.Workspace, skill)
	if err != nil {
		// The record could not be read. Same fail-closed answer as an absent path:
		// a precondition that could not be checked has not passed.
		return refuse(fmt.Sprintf(
			"this rule requires the %q skill to have been loaded first, but this session's record could not be read (%v), "+
				"so whether it was loaded is unknown. Refusing: a precondition that could not be checked is not a precondition that passed.",
			skill, err)), nil
	}
	if !loaded {
		return refuse(skillRemedy(skill)), nil
	}

	var missing []string
	for _, file := range files {
		read, err := r.subpageRead(req.TranscriptPath, req.Workspace, skill, file)
		if err != nil {
			return refuse(fmt.Sprintf(
				"this rule requires %q (inside the %q skill) to have been read first, but this session's record could not be read (%v), "+
					"so whether it was read is unknown. Refusing: a precondition that could not be checked is not a precondition that passed.",
				file, skill, err)), nil
		}
		if !read {
			missing = append(missing, file)
		}
	}
	if len(missing) > 0 {
		return refuse(subpagesRemedy(skill, missing, req.Workspace)), nil
	}
	return pass(), nil
}

// skillRemedy is what a missing-skill refusal tells the agent — the one
// instruction that clears it.
//
// It names the skill and says the check reads the RECORD, not a claim, because
// the whole value of a native skill prerequisite over prose is that "I read the
// skill" is not what is checked. The Skill-tool-use wording is carried
// word-for-word from require-skill.sh's own refusal, which had been tuned
// against real agents; the second sentence names the read-detection half
// (skillLoadedInTrajectory) so a refusal does not describe only one of the two
// ways to clear it.
func skillRemedy(skill string) string {
	return fmt.Sprintf(
		"SKILL REQUIRED: this action requires the %q skill to have been loaded first, and this session's record holds no Skill tool_use naming it, "+
			"and no Read or file-reading Bash command (cat, head, …) on its SKILL.md either. "+
			"Invoke the Skill tool with skill %q, then retry — or read the skill's own SKILL.md file directly. "+
			"Stating that you have read it is not what is checked — the session's own record is.",
		skill, skill)
}

// subpagesRemedy is what a missing-subpage refusal tells the agent — the one
// message that clears it, for EVERY unmet files entry at once rather than one
// at a time.
//
// Refusing on only the first missing file (the earlier shape) meant an agent
// that read file[0] after a first refusal hit file[1]'s refusal on its very
// next write attempt, and so on — the same total number of refusals as
// naming them all up front, just spread across turns instead of listed once,
// each one costing a full write-refuse-retry cycle. Naming every missing
// file in one message lets the agent clear all of them before its next
// attempt.
//
// Each file gets a concrete path, the same way the single-file remedy did:
// the first candidate that actually exists on disk (a project skill outranks
// a plugin-shipped one of the same name, the same resolution order
// SkillSubpagePaths returns them in); with none resolvable, the first
// candidate is named anyway — a path to try beats no path at all.
func subpagesRemedy(skill string, files []string, workspace string) string {
	lines := make([]string, 0, len(files))
	for _, file := range files {
		path := file
		candidates := SkillSubpagePaths(workspace, skill, file)
		if len(candidates) > 0 {
			path = candidates[0]
			for _, c := range candidates {
				if _, err := os.Stat(c); err == nil {
					path = c
					break
				}
			}
		}
		lines = append(lines, fmt.Sprintf("  - %s: %s", file, path))
	}
	if len(files) == 1 {
		return fmt.Sprintf(
			"READ REQUIRED: this action requires %q (inside the %q skill) to have been read first, and this session's record holds no "+
				"Read or file-reading Bash command (cat, head, …) on it. Read it directly:\n%s\n"+
				"Stating that you have read it is not what is checked — the session's own record is.",
			files[0], skill, lines[0])
	}
	return fmt.Sprintf(
		"READ REQUIRED: this action requires the following pages inside the %q skill to have been read first, and this session's "+
			"record holds no Read or file-reading Bash command (cat, head, …) on any of them:\n%s\n"+
			"Read all of them directly, at the paths above. Stating that you have read them is not what is checked — the session's own record is.",
		skill, strings.Join(lines, "\n"))
}

// checkContext refuses unless the named context is active in the state map the
// caller threaded in.
//
// For THIS slice a `{context}` prerequisite is satisfied when the named context's
// entry in the `context` map is active. The spec frames `{context}` as an ENGINE
// ORDERING guarantee — the named context must have run its enter this cycle
// before this rule — and the full lifecycle that makes that ordering happen is
// the next slice. Here the runner reads whatever context state it was given: the
// next slice populates the map, and this evaluation does not change when it does.
//
// A context named in `require` always resolves to a real declaration (the loader
// refuses an unknown context name), so an absent entry is not a typo — it is a
// context that has not entered. That refuses, with a remedy naming the context,
// because a rule that depends on a context having run must not admit the action
// when it has not.
func (r Runner) checkContext(req Request, name string) Verdict {
	st, ok := req.Context[name]
	if ok && st.Active {
		return pass()
	}
	return refuse(fmt.Sprintf(
		"this rule requires the %q context to be active first, and it is not. "+
			"That context enters on its own triggers; nothing to act on directly — if this is unexpected, the context did not recognise this cycle as one it applies to.",
		name))
}

// skillLoadedInTrajectory is the production skillLoaded: it reads the session's
// own record — the record for the SESSION now doing the writing, be that the
// root or a sub-agent — and reports whether a Skill tool_use naming the skill was
// made on that record's own line of work.
//
// This is the native form of what the deprecated require-skill.sh did with
// `sr-session query` and jq. The mechanics it settled are kept:
//
//   - `type == "assistant"` entries only — a Skill call is a tool_use an assistant
//     turn makes.
//   - a tool_use block whose name is exactly "Skill".
//   - whose `input.skill` equals the required skill. The field is `skill`, read
//     outright: the spec declares SkillToolInput and the measurement behind it
//     (664 Skill calls, `skill` on every one) settles that there is no second
//     field name to hedge against.
//
// # Whose entries count as "the writing line of work"
//
// A root session's Stop reads its OWN transcript, and IsSidechain there marks
// entries belonging to a DELEGATED sub-agent — a different line of work than the
// one now writing, so those are excluded. A sub-agent's SubagentStop reads a
// DIFFERENT file: its OWN transcript (HookPayload.record() resolves to
// AgentTranscriptPath for that call), and every entry a harness writes there
// carries IsSidechain true — that is simply how a harness marks "this file is a
// sub-agent's", not a marker that some entries in it belong to a further-delegated
// child. Measured in transcript.SubagentTranscriptPath's own survey: "every
// record carried isSidechain true" for the 331 sub-agent files sampled, with none
// carrying a LogicalParentUUID that would identify a nested grandchild.
//
// So IsSidechain cannot be read the same way in both files. Excluding it
// unconditionally emptied a sub-agent's OWN Skill tool_use from its OWN
// trajectory — the record a SubagentStop check is actually asked about — which
// made `require: [{skill: ...}]` unsatisfiable there no matter how many times or
// how recently the sub-agent invoked the Skill tool. The fix is to key the
// exclusion on the FILE, not the flag: skip sidechain entries only when reading a
// trajectory that is not itself a sub-agent's own record (transcript.
// IsSubagentTranscript decides which file this is, once, before the walk). Inside
// a sub-agent's own file every entry already belongs to the line of work now
// writing, so nothing is excluded there.
//
// The record is read whole via transcript.Read, which already skips the harness's
// bookkeeping lines. An unreadable record is an error the caller turns into a
// fail-closed refusal, not a false — "could not read" and "was not loaded" are
// different facts and only the second is an answer.
//
// # The second way to satisfy this: reading the skill's own file directly
//
// A Skill tool_use is not the only trajectory evidence that a skill was
// genuinely consulted. An agent that opened the skill's own SKILL.md — with the
// Read tool, or with a Bash command that reads it whole (`cat`, `head`, `less`,
// …, see commandmod.ReadsFile) — has looked at exactly the same content loading
// the skill would have shown it, through a channel the record can verify just as
// concretely as a Skill tool_use: the file path is either named on a Read
// tool_use's `file_path`, or found by commandmod's own command-line parser
// inside a Bash tool_use's `command`. Neither is a claim — both are the kind of
// native, record-grounded fact `{skill}` exists to check instead of prose.
//
// This is why the walk below checks BOTH per entry in one pass, rather than two
// separate trajectory reads: same entries, same sidechain-exclusion rule, same
// tool-call extraction, so the two ways to satisfy the prerequisite cannot
// disagree about which entries are "this line of work".
//
// workspace resolves the skill NAME to the file path(s) it could be loaded
// from (SkillFilePaths) — a Read or Bash tool_use names a PATH, not a skill
// name, so the connection has to be made before any entry is read. An empty
// workspace (no project root resolvable) yields no candidate paths, and the
// read-file half of this check then finds nothing to match — degrading to the
// Skill-tool-use check alone, never a refusal of its own: a workspace that
// could not be resolved is a fact about the session, already surfaced
// elsewhere, and is not this function's fact to refuse a second time.
func skillLoadedInTrajectory(transcriptPath, workspace, skill string) (bool, error) {
	entries, err := transcript.Read(transcriptPath)
	if err != nil {
		return false, err
	}
	skillPaths := SkillFilePaths(workspace, skill)

	found := false
	err = walkWritingLineOfWork(transcriptPath, entries, func(call transcript.ToolCall) bool {
		if call.Name == "Skill" && skillNameMatches(call, skill) {
			found = true
			return true
		}
		if pathReadByEitherTool(call, skillPaths) {
			found = true
			return true
		}
		return false
	})
	return found, err
}

// subpageReadInTrajectory reports whether a FILE inside skill `skill` (file,
// relative to the skill's own directory) was read directly — a Read tool_use
// on one of its candidate paths (SkillSubpagePaths), or a file-reading Bash
// command against one — on the session's own writing line of work.
//
// Unlike skillLoadedInTrajectory, there is no Skill-tool-use branch here: the
// Skill tool loads a skill's SKILL.md, never a specific subpage by name, so
// invoking it is evidence the SKILL ITSELF was loaded (skillLoadedInTrajectory
// already covers that), not that any particular page inside it was opened.
func subpageReadInTrajectory(transcriptPath, workspace, skill, file string) (bool, error) {
	entries, err := transcript.Read(transcriptPath)
	if err != nil {
		return false, err
	}
	paths := SkillSubpagePaths(workspace, skill, file)

	found := false
	err = walkWritingLineOfWork(transcriptPath, entries, func(call transcript.ToolCall) bool {
		if pathReadByEitherTool(call, paths) {
			found = true
			return true
		}
		return false
	})
	return found, err
}

// walkWritingLineOfWork walks entries — already read from transcriptPath — and
// calls onCall for every tool_use on the session's own writing line of work
// (see the sidechain-exclusion note above skillLoadedInTrajectory), stopping
// early the moment onCall reports a match. Shared by skillLoadedInTrajectory
// and subpageReadInTrajectory so the two cannot disagree about which entries
// are "this line of work" or how a tool_use is extracted from one.
func walkWritingLineOfWork(transcriptPath string, entries []transcript.Entry, onCall func(transcript.ToolCall) bool) error {
	// Decided once, from the FILE, not per entry: a sub-agent's own transcript
	// marks every entry IsSidechain, so treating that flag as "belongs to a
	// delegated child" inside that very file would exclude everything in it. See
	// the note above skillLoadedInTrajectory.
	excludeSidechain := !transcript.IsSubagentTranscript(transcriptPath)
	for _, e := range entries {
		if e.Type != transcript.EntryAssistant {
			continue
		}
		if excludeSidechain && e.IsSidechain {
			continue
		}
		for _, call := range transcript.ToolCalls(e) {
			if onCall(call) {
				return nil
			}
		}
	}
	return nil
}

// pathReadByEitherTool reports whether call is a Read tool_use naming one of
// paths, or a Bash-shaped tool_use whose command reads one of them whole — the
// two ways this package treats a file as "genuinely read", shared by the
// skill-file check and the subpage check.
func pathReadByEitherTool(call transcript.ToolCall, paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	if call.Name == "Read" {
		return pathReadByReadTool(call, paths)
	}
	return commandReadsAnyOf(call, paths)
}

// pathReadByReadTool reports whether a Read tool_use's `file_path` names one
// of the candidate skill paths — compared after ResolveExistingPrefix, not by
// literal string equality, so two symlink spellings of the same file (macOS's
// /var vs /private/var: Request.Workspace is built from a git root, which
// `git rev-parse --show-toplevel` resolves, while the AGENT'S own file_path is
// whatever path it was actually handed) still match. Measured against a real
// e2e run: an agent Read the skill's own subpage at the exact path a refusal
// had just told it to, and the bare comparison still refused it — the read
// undeniably happened, and the check said otherwise.
//
// `file_path` is the same key filemod's own write-tool reading uses
// (pendingshape.go's FilePath) — one field name, one convention, across every
// tool that names a file directly rather than through a command line.
func pathReadByReadTool(call transcript.ToolCall, skillPaths []string) bool {
	var in struct {
		FilePath string `json:"file_path"`
	}
	if json.Unmarshal(call.Input, &in) != nil || in.FilePath == "" {
		return false
	}
	return pathMatchesAnyOf(in.FilePath, skillPaths)
}

// pathMatchesAnyOf reports whether path resolves (ResolveExistingPrefix) to
// the same place as any of candidates, each also resolved. Both sides are
// resolved, not just one, because either can carry the symlinked spelling
// depending on how it was produced — a candidate built from Request.Workspace
// (a resolved git root) against an agent-reported path that is not, or the
// reverse.
func pathMatchesAnyOf(path string, candidates []string) bool {
	resolved := ResolveExistingPrefix(path)
	for _, c := range candidates {
		if resolved == ResolveExistingPrefix(c) {
			return true
		}
	}
	return false
}

// commandReadsAnyOf reports whether a tool_use carrying a shell command
// (`command`, the shape any Bash-like tool uses — see commandmod's own Pending)
// reads any of the candidate skill paths whole.
//
// Read the same way commandmod's own module reads a pending command's
// arguments: decode `command` and hand it to the parser. Not gated on the tool
// NAME the way commandmod's Extract is (HarnessCommandTools) — a name allowlist
// exists there to bound which tools produce a PreCommandInvoke event system-wide,
// a much bigger surface than this one prerequisite check. Here a tool_use naming
// no `command` field simply contributes nothing, which is the same "not a
// shape this reads" answer arrived at without a name gate at all — no drift risk
// results, because the failure mode of skipping the gate is a wasted parse on an
// unrelated tool's input, not a wrongly-satisfied prerequisite.
//
// Each of commandmod's own extracted targets is compared through
// pathMatchesAnyOf, not commandmod.ReadsFile's literal equality — the same
// symlink-spelling gap pathReadByReadTool fixes (its own doc comment has the
// measurement), and a `cat`'d path is exactly as exposed to it as a Read
// tool_use's file_path is.
func commandReadsAnyOf(call transcript.ToolCall, skillPaths []string) bool {
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(call.Input, &in) != nil || in.Command == "" {
		return false
	}
	for _, target := range commandmod.ReadTargets(in.Command) {
		if pathMatchesAnyOf(target.Path, skillPaths) {
			return true
		}
	}
	return false
}

// skillLoaded is the production Runner.skillLoaded: skillLoadedInTrajectory
// under the name Runner.withDefaults assigns to the zero-value Runner.
//
// A thin wrapper rather than assigning skillLoadedInTrajectory directly so the
// two names read distinctly at each call site — skillLoaded is what a Runner
// calls, skillLoadedInTrajectory is what does the work — the same split
// dispatch.go's own doc comment on the Runner field describes.
func skillLoaded(transcriptPath, workspace, skill string) (bool, error) {
	return skillLoadedInTrajectory(transcriptPath, workspace, skill)
}

// subpageRead is the production Runner.subpageRead: subpageReadInTrajectory
// under the name Runner.withDefaults assigns to the zero-value Runner. Same
// split as skillLoaded/skillLoadedInTrajectory above.
func subpageRead(transcriptPath, workspace, skill, file string) (bool, error) {
	return subpageReadInTrajectory(transcriptPath, workspace, skill, file)
}
