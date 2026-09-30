package dispatch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/commandmod"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/grounding"
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
	hint := ""
	if p.When != "" {
		applies, h, err := r.prerequisiteApplies(req, p.When)
		if err != nil {
			return Verdict{}, err
		}
		if !applies {
			return pass(), nil
		}
		hint = h
	}
	if p.Citation != nil {
		return r.checkCitation(req, p, hint)
	}
	v, err := r.checkUnconditional(req, p)
	if err == nil && v.Refused && hint != "" {
		// The rule's own words on how to meet it here — the engine's remedy is
		// generic, the `when` script knows the case.
		v.Reason += "\n" + hint
	}
	return v, err
}

// checkUnconditional evaluates a prerequisite that applies.
func (r Runner) checkUnconditional(req Request, p declaration.Prerequisite) (Verdict, error) {
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

// prerequisiteApplies runs a prerequisite's `when` script against the check
// payload. Only exit 1 waives the prerequisite; every other outcome — exit 0,
// another code, a script that could not run or did not answer — applies it,
// so a condition that could not be decided never lifts a requirement.
//
// A script that applies it may print `{"hint": "..."}` on stdout: the rule's
// own advice for meeting the requirement in this case, appended to the refusal.
// Anything else on stdout is ignored.
func (r Runner) prerequisiteApplies(req Request, when string) (bool, string, error) {
	payload, err := r.checkPayloadJSON(req)
	if err != nil {
		return false, "", err
	}
	res, err := r.runScript(scriptCall{
		Dir:            req.Dir,
		Script:         when,
		Stdin:          payload,
		GuardName:      req.GuardName,
		Workspace:      req.Workspace,
		SessionID:      req.SessionID,
		TranscriptPath: req.TranscriptPath,
		LaunchedBy:     req.LaunchedBy,
		Env:            req.Env,
	})
	if err != nil {
		return false, "", err
	}
	if !res.Passed && res.Code == 1 {
		return false, "", nil
	}
	var out struct {
		Hint string `json:"hint"`
	}
	if res.Passed && json.Unmarshal(trimSpace(res.Stdout), &out) == nil {
		return true, strings.TrimSpace(out.Hint), nil
	}
	return true, "", nil
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
	paths := append(SkillSubpagePaths(workspace, skill, file), announcedSkillPaths(entries, skill, file)...)

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

// skillBaseDirLine is how Claude Code announces where it loaded a skill from,
// the first line of the skill body it writes into the record.
const skillBaseDirLine = "Base directory for this skill: "

// announcedSkillPaths is file inside every directory the harness announced it
// loaded skill from. A directory-sourced marketplace serves a plugin's skill
// from its source tree, not the plugin cache SkillSubpagePaths resolves, and
// the agent reads the page where the skill says it lives. Only a harness-written
// (isMeta) user entry counts: text an agent printed into a tool result cannot
// point the check at a page it wrote itself.
func announcedSkillPaths(entries []transcript.Entry, skill, file string) []string {
	name := skill
	if i := strings.LastIndex(name, ":"); i >= 0 {
		name = name[i+1:]
	}
	var out []string
	for _, e := range entries {
		if e.Type != transcript.EntryUser || !e.IsMeta || e.IsSidechain {
			continue
		}
		var msg struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(e.Message, &msg) != nil {
			continue
		}
		for _, b := range msg.Content {
			if b.Type != "text" || !strings.HasPrefix(b.Text, skillBaseDirLine) {
				continue
			}
			dir, _, _ := strings.Cut(strings.TrimPrefix(b.Text, skillBaseDirLine), "\n")
			dir = strings.TrimSpace(dir)
			if filepath.IsAbs(dir) && filepath.Base(dir) == name {
				out = append(out, filepath.Join(dir, file))
			}
		}
	}
	return out
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

// checkCitation refuses unless the event carries at least one citation that
// resolved in a pool the prerequisite accepts — and, at Stop, unless every part
// of the file's change the agent made that no cited change in those pools made
// (req.History) is one the prerequisite's `when` waives.
//
// It reads `citations` off the event and nothing else. The session resolved
// every citation against its own record before dispatch (services/sr-session
// grounding.go), so presence here IS existence: a quote that did not resolve
// never became a citation. Whether it grounds THIS change is for the rule's
// judge, which reads the same field.
func (r Runner) checkCitation(req Request, p declaration.Prerequisite, hint string) (Verdict, error) {
	pools := p.Citation.Pools()
	if !citedIn(req.Event, pools) {
		return refuse(citationRemedy(req.Event.Kind, req.Event.Fields, pools, hint) + subagentCitationNote(req.TranscriptPath, pools)), nil
	}
	if req.History == nil {
		return pass(), nil
	}
	for _, u := range req.History.uncitedParts(pools) {
		if p.When != "" {
			part := req
			part.Event = uncitedEvent(req.Event, u)
			part.History = nil
			applies, _, err := r.prerequisiteApplies(part, p.When)
			if err != nil {
				return Verdict{}, err
			}
			if !applies {
				continue
			}
		}
		return refuse(uncitedRemedy(req.Event.Fields, pools, u)), nil
	}
	return pass(), nil
}

// citedIn reports whether e carries a citation that resolved in one of pools.
func citedIn(e event.Event, pools []transcript.SourceType) bool {
	for _, cit := range grounding.FromWire(e.Fields[grounding.FieldCitations]) {
		for _, got := range cit.SourceTypes {
			if slices.Contains(pools, got) {
				return true
			}
		}
	}
	return false
}

// uncitedEvent is e narrowed to one uncited part of its change: the same Post
// event, its old and new content (and markers) those of that part, its kind
// what that part did to the file — and no citations, since none rode on it.
func uncitedEvent(e event.Event, u UncitedChange) event.Event {
	kind := declaration.KindPostFileUpdate
	switch {
	case !u.FromExists:
		kind = declaration.KindPostFileCreate
	case !u.ToExists:
		kind = declaration.KindPostFileDelete
	}
	path, _ := e.Fields[filemod.FieldPath].(string)
	fe := filemod.FileEvent{Path: path, ResultKnown: true}
	if u.FromExists {
		fe.OldContent, fe.OldMarkers = u.From, filemod.Scan(u.From)
	}
	if u.ToExists {
		fe.NewContent, fe.NewMarkers = u.To, filemod.Scan(u.To)
	}
	part := fe.Event(kind)
	fields := make(map[string]any, len(e.Fields))
	for k, v := range e.Fields {
		fields[k] = v
	}
	for _, k := range []string{filemod.FieldOldContent, filemod.FieldNewContent, filemod.FieldOldMarkers, filemod.FieldNewMarkers} {
		delete(fields, k)
		if v, ok := part.Fields[k]; ok {
			fields[k] = v
		}
	}
	fields[grounding.FieldCitations] = grounding.ToWire(nil)
	if _, declared := part.Fields[filemod.FieldNewContentKnown]; declared {
		fields[filemod.FieldNewContentKnown] = u.ToKnown
	} else {
		delete(fields, filemod.FieldNewContentKnown)
	}
	return event.Event{Kind: kind, Fields: fields}
}

// uncitedRemedy says that the file carries citations, but also a change the
// agent made without one in these pools, which they do not ground — and the
// one command that settles it: restating the whole file, cited. An edit would
// ground only its own part and leave the uncited one standing.
func uncitedRemedy(fields map[string]any, pools []transcript.SourceType, u UncitedChange) string {
	names := make([]string, len(pools))
	for i, p := range pools {
		names[i] = string(p)
	}
	flag := "--cite:" + strings.Join(names, ",")
	path, _ := fields["path"].(string)
	if u.BetweenTurns {
		by := u.By
		if by == "" {
			by = "background work"
		}
		return fmt.Sprintf("%s changed after your last Stop, while work you started earlier this session may still have been running (%s). "+
			"A change that lands then is charged to you like one you made, since it may be that work's, and it carries no citation. "+
			"If it is yours, restate the whole file with a cited sr-file write, citing %s (%s):\n"+
			"  sr-file write %s %s '<exact quote>' <<'EOF'\n  <the whole file>\n  EOF",
			path, by, citedWhat(pools), flag, path, flag)
	}
	return fmt.Sprintf("%s was changed without a citation this session (a Write, an Edit, a command, or an sr-file call citing another pool), "+
		"and a citation grounds only the change it rode on. Restate the whole file with a cited sr-file write, citing %s (%s) — "+
		"it grounds the file as it leaves it:\n"+
		"  sr-file write %s %s '<exact quote>' <<'EOF'\n  <the whole file>\n  EOF",
		path, citedWhat(pools), flag, path, flag)
}

// subagentCitationNote tells a sub-agent, refused for want of the user's words,
// that it never saw them: its prompt is the parent agent's. Empty for the root,
// and for a requirement that does not accept the user pool.
func subagentCitationNote(transcriptPath string, pools []transcript.SourceType) string {
	if transcriptPath == "" || !slices.Contains(pools, transcript.SourceUser) || !transcript.IsSubagentTranscript(transcriptPath) {
		return ""
	}
	return "\n" + transcript.SubagentUserAdvice
}

// citedWhat names, in plain words, what a citation in these pools quotes.
func citedWhat(pools []transcript.SourceType) string {
	var user, tool bool
	for _, p := range pools {
		user = user || p == transcript.SourceUser
		tool = tool || p == transcript.SourceToolResult
	}
	switch {
	case user && tool:
		return "the user's words or a tool's output from this session"
	case tool:
		return "a tool's output from this session"
	}
	return "the user's own words"
}

// citationRemedy says how to ground the action this event describes: what it
// must cite and with which flag, then how — always with a command the agent can
// run.
//
// A rule's hint (from its `when` script) is its advice for this case. When the
// hint spells the command itself (an `sr-file ` line for a file change, a
// `trajectory cite` chain for a command), it IS the how and replaces the
// generic forms, which would only restate it less precisely. When it gives
// advice without a command ("append instead of rewriting"), the engine's own
// runnable form for this kind of change comes first and the hint follows it:
// a refusal that says what to do but not how to run it leaves the agent
// guessing at a command.
func citationRemedy(kind string, fields map[string]any, pools []transcript.SourceType, hint string) string {
	names := make([]string, len(pools))
	for i, p := range pools {
		names[i] = string(p)
	}
	flag := "--cite:" + strings.Join(names, ",")
	what := citedWhat(pools)
	path, _ := fields["path"].(string)

	switch kind {
	case changeset.Kind:
		// A changeset carries its grounding in the commits' messages, next to the
		// change it grounds; there is no command to chain.
		trailer := "Sloprail-Cites-User"
		if len(pools) == 1 && pools[0] == transcript.SourceToolResult {
			trailer = "Sloprail-Cites-Tool"
		}
		how := fmt.Sprintf("Amend or add a commit in this range whose message carries a trailer quoting %s exactly:\n"+
			"  %s: <exact quote>", what, trailer)
		if hint != "" {
			how += "\n" + hint
		}
		subject := "this change must cite " + what + ", and no commit in its range carries a citation that resolves."
		if path != "" {
			subject = path + " must cite " + what + " in the commit that last changed it, and that commit carries none that resolves."
			how = fmt.Sprintf("Amend that commit, or add a commit that changes %s, with a trailer quoting %s exactly:\n  %s: <exact quote>", path, what, trailer)
			if hint != "" {
				how += "\n" + hint
			}
		}
		return fmt.Sprintf("%s\n%s\n"+
			"The quote must match exactly one entry of this session — check one with `sr-session trajectory cite '<quote>'`.", subject, how)
	case declaration.KindPreCommandInvoke:
		how := fmt.Sprintf("Chain a cite in front of it, quoting the exact words:\n"+
			"  sr-session trajectory cite --source-types %s '<exact quote>' && <the command>", strings.Join(names, ","))
		how = withHint(how, hint, citeCommand)
		return fmt.Sprintf("this command must cite %s, and it carries none that resolves.\n%s\n"+
			"The quote must match exactly one entry of this session; run the cite part alone first to check it.",
			what, how)
	case declaration.KindPostFileCreate, declaration.KindPostFileUpdate, declaration.KindPostFileDelete:
		how := withHint("Redo the change with sr-file, citing what it rests on:\n"+sfFileForms(kind, path, flag, hint != ""), hint, fileCommand)
		return fmt.Sprintf("%s was changed without citing %s (%s).\n%s\n%s", path, what, flag, how, runOnItsOwn)
	}
	how := withHint("Make it with sr-file, which carries the citation on the command (never in the file):\n"+sfFileForms(kind, path, flag, hint != ""), hint, fileCommand)
	return fmt.Sprintf("this change to %s must cite %s (%s), and it carries none that resolves.\n%s\n%s "+
		"Single-quote the quote; it must match exactly one entry of this session — check one with `sr-session trajectory cite '<quote>'`.",
		path, what, flag, how, runOnItsOwn)
}

// runOnItsOwn is how sr-file must be run for its change to be computed before
// it runs — and so for its citations to count.
const runOnItsOwn = "Run sr-file ON ITS OWN in the command (call it by its bare name `sr-file` — it must be on PATH, check `command -v sr-file` — not a path; nothing else in the line but sr-file calls, &&, and echo; no cd, no export or VAR= prefix, no $ expansion, no unquoted glob or brace (* ? [ { ^ # ~name) — quote every value verbatim) so its result can be checked before it runs."

// withHint is the how of a remedy given a rule's hint: the hint alone when one
// of its lines IS the runnable command (starts with it, once indented space is
// trimmed — a hint merely mentioning `sr-file ` in prose does not qualify),
// else the engine's form and then the hint.
func withHint(form, hint string, command *regexp.Regexp) string {
	if hint == "" {
		return form
	}
	for _, line := range strings.Split(hint, "\n") {
		if command.MatchString(strings.TrimSpace(line)) {
			return hint
		}
	}
	return form + "\n" + hint
}

var (
	// fileCommand is a line that runs a grounded sr-file verb.
	fileCommand = regexp.MustCompile(`^sr-file (write|edit|delete) `)
	// citeCommand is a line that runs a cite chain.
	citeCommand = regexp.MustCompile(`^sr-session trajectory cite `)
)

// sfFileForms is the sr-file command lines that make a change of this kind to
// path: every form when narrow is false; when it is true (a rule's hint
// follows), only those for this kind of change — a write for a create, an edit
// or a write for an update, a delete for a delete.
func sfFileForms(kind, path, flag string, narrow bool) string {
	edit := fmt.Sprintf("  sr-file edit %s --old-string '<old>' --new-string '<new>' [--replace-all] %s '<exact quote>'", path, flag)
	write := fmt.Sprintf("  sr-file write %s %s '<exact quote>' <<'EOF' ... EOF", path, flag)
	del := fmt.Sprintf("  sr-file delete %s %s '<exact quote>'", path, flag)
	if narrow {
		switch kind {
		case declaration.KindPreFileCreate, declaration.KindPostFileCreate:
			return write
		case declaration.KindPreFileUpdate, declaration.KindPostFileUpdate:
			return edit + "\n" + write
		case declaration.KindPreFileDelete, declaration.KindPostFileDelete:
			return del
		}
	}
	return edit + "\n" + write + "\n" + del
}
