package dispatch

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pmezard/go-difflib/difflib"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/natures"
	"github.com/sloprail/sloprail/internal/transcript"
)

// This file is the `checks` half of the check-runner: one Check at a time, in the
// order the caller listed them, first-refusal-ends-it (dot-dir-file-store/main.tsp
// Check). A check is a `script` or a `judge`, never both on one entry (the
// validator's exactly-one-of), and a `judge` may carry a `prepare` that runs
// first and feeds its result into the judge's prompt.
//
// The payload a check receives is assembled here to the spec's exact shape, keyed
// by the request's Nature: a file-guard's CheckPayload / FileJudgeInput, a gate's
// GateCheckPayload / GateJudgeInput. Assembling it in one place is what keeps a
// script's stdin and a judge's template variables reading the same facts.

// runCheck runs one check and returns its verdict.
//
// A script check is the deterministic half: the payload on stdin, pass/fail by
// exit code. A judge check is the model half: prepare (if set) runs first and its
// additionalContext feeds the judge's prompt, then the judge renders and is asked
// for a pass/fail verdict. The two are dispatched on which field the check set.
func (r Runner) runCheck(req Request, c declaration.Check) (Verdict, error) {
	switch {
	case c.Script != "":
		return r.runScriptCheck(req, c)
	case c.Judge != "":
		return r.runJudgeCheck(req, c)
	default:
		// A loaded check always sets exactly one of script/judge (the validator
		// refuses an empty one), so this is unreachable for a real rule. Treated as
		// a fail-closed refusal rather than a silent pass: a check that decided
		// nothing must not read as approval.
		return refuse("this check names neither a script nor a judge, so it decided nothing; refusing rather than treating an empty check as a pass"), nil
	}
}

// runScriptCheck runs a script check: the nature's CheckPayload on stdin, exit
// code the verdict.
//
// Zero exits and permits; non-zero refuses, carrying whatever the script said as
// the reason — the same convention every check in a shell already uses, and the
// same one the old-format hooks use. A script that could not be RUN at all is a
// refusal too (fail-closed), so a missing or non-executable script blocks rather
// than silently admitting.
func (r Runner) runScriptCheck(req Request, c declaration.Check) (Verdict, error) {
	payload, err := r.checkPayloadJSON(req)
	if err != nil {
		return Verdict{}, err
	}
	res, err := r.runScript(scriptCall{
		Dir:            req.Dir,
		Script:         c.Script,
		Stdin:          payload,
		GuardName:      req.GuardName,
		Workspace:      req.Workspace,
		SessionID:      req.SessionID,
		TranscriptPath: req.TranscriptPath,
		LaunchedBy:     req.LaunchedBy,
		Env:            req.Env,
	})
	if err != nil {
		return Verdict{}, err
	}
	if res.Passed {
		return pass(), nil
	}
	return refuse(res.Reason), nil
}

// runJudgeCheck runs a judge check: prepare (if set) first, then — unless prepare
// asked to skip — the model.
//
// prepare, when set, runs first and resolves to one of the outcomes catalogued on
// declaration.PreparedOutcome:
//
//   - it FAILS to run (or its stdout is malformed) -> the check fails closed,
//     carrying prepare's own words, the same as a script refusal would (the spec:
//     "a prepare failure fails the check"). The model is never asked.
//   - it asks to SKIP (stdout `skip: true`) -> this check ABSTAINS: the model is
//     NOT invoked and the check reaches no verdict of its own. Returning abstain()
//     — not pass() — is deliberate: a skip must not stand in for an affirmative
//     verdict that could mask a LATER check that would refuse. The check drops out
//     of the chain and whatever else the guard says (or the default permit)
//     decides. The model call is the expensive part and skipping it is the whole
//     point; its `additionalContext` is moot (there is no prompt to fold it into)
//     and is not read here.
//   - it runs cleanly WITHOUT a skip -> its `additionalContext` (and only that key)
//     is added to the judge's input under `additionalContext`, alongside the
//     standard payload rather than replacing it, and the model runs. Empty stdout
//     is this case with no additional context — silence lets the judge run, it is
//     not a skip.
func (r Runner) runJudgeCheck(req Request, c declaration.Check) (Verdict, error) {
	prepared, v, err := r.PrepareJudge(req, c)
	if err != nil || v.Refused {
		return v, err
	}
	if prepared.Skip {
		// prepare inspected the subject and decided the judge does not apply
		// here: ABSTAIN WITHOUT a model call. Returning abstain() before
		// judgeInputJSON/runJudge is what makes "skip" mean no model invocation
		// at all — nothing renders the template and no agent is spawned — while
		// leaving the guard's decision to the remaining checks rather than
		// forcing a pass that would mask a later refusal.
		return abstain(), nil
	}
	return r.Judge(req, c, prepared)
}

// Prepared is what a judge check's prepare step concluded, ready for the judge.
type Prepared struct {
	// Skip: prepare asked not to run the judge; the check abstains.
	Skip bool
	// Context is prepare's additionalContext, folded into the judge's input.
	Context declaration.PreparedContext
	// Fingerprint is prepare's optional "fingerprint" string: what the judge's verdict
	// depends on besides its prompt, part of the cache key.
	Fingerprint string
}

// PrepareJudge runs a judge check's prepare step, when it has one. A refused
// verdict means prepare failed (the check fails closed, carrying prepare's own
// words); otherwise Prepared says whether to skip the judge and what context to
// give it. Split from Judge so a caller that caches verdicts can fingerprint what
// the judge is about to receive — including what prepare inlined — before paying
// for the model.
func (r Runner) PrepareJudge(req Request, c declaration.Check) (Prepared, Verdict, error) {
	r = r.withDefaults()
	if c.Prepare == "" {
		return Prepared{}, pass(), nil
	}
	prepared, v, err := r.runPrepare(req, c.Prepare)
	if err != nil {
		return Prepared{}, Verdict{}, err
	}
	if v.Refused {
		return Prepared{}, v, nil
	}
	return Prepared{Skip: prepared.Skip, Context: prepared.Context, Fingerprint: prepared.Fingerprint}, pass(), nil
}

// RenderJudge is the judge's fully rendered prompt (the template with the slice and
// prepare's additionalContext folded in), exactly as Judge will render it: what a cache keys
// a verdict on. A non-empty refusal is the reason the prompt cannot be rendered; Judge
// refuses with the same words.
func (r Runner) RenderJudge(req Request, c declaration.Check, p Prepared) (rendered, refusal string, err error) {
	call, v, err := r.judgeCall(req, c, p)
	if err != nil || v.Refused {
		return "", v.Reason, err
	}
	return renderJudgePrompt(call)
}

// Judge asks the model about one judge check, after prepare.
func (r Runner) Judge(req Request, c declaration.Check, p Prepared) (Verdict, error) {
	r = r.withDefaults()
	call, v, err := r.judgeCall(req, c, p)
	if err != nil || v.Refused {
		return v, err
	}
	return r.runJudge(call)
}

func (r Runner) judgeCall(req Request, c declaration.Check, p Prepared) (judgeCall, Verdict, error) {
	r = r.withDefaults()
	input, err := r.judgeInputJSON(req, p.Context)
	if err != nil {
		return judgeCall{}, Verdict{}, err
	}

	// The check's own timeout, parsed from its duration string. The loader
	// already validated it parses to a positive duration, so a loaded rule never
	// fails here; a malformed value that somehow reached this point is treated as
	// fail-closed (refuse) rather than silently falling back to the default — a
	// judge's timeout is a safety bound, and running one the author's config did
	// not actually specify is the wrong direction to guess.
	timeout, err := checkTimeout(c)
	if err != nil {
		return judgeCall{}, refuse(fmt.Sprintf(
			"the judge's timeout %q could not be read (%v); refusing rather than judging under a timeout the rule did not specify", c.Timeout, err)), nil
	}

	return judgeCall{
		Dir:             req.Dir,
		Template:        c.Judge,
		InputJSON:       input,
		GuardName:       req.GuardName,
		Model:           c.Model,
		Timeout:         timeout,
		LaunchedBy:      req.LaunchedBy,
		AllowedTools:    c.AllowedTools,
		DisallowedTools: c.DisallowedTools,
		Workspace:       req.judgeProject(),
		Env:             req.Env,
	}, Verdict{}, nil
}

// RunScript runs one script check: the payload on stdin, exit code the verdict.
func (r Runner) RunScript(req Request, c declaration.Check) (Verdict, error) {
	return r.withDefaults().runScriptCheck(req, c)
}

// checkTimeout parses a check's `timeout` duration string, or returns 0 (meaning
// "the engine default") when the check names none. A non-empty value that does
// not parse is an error the caller turns into a fail-closed refusal — but the
// loader validates this field, so that path is defensive.
func checkTimeout(c declaration.Check) (time.Duration, error) {
	if c.Timeout == "" {
		return 0, nil
	}
	return time.ParseDuration(strings.TrimSpace(c.Timeout))
}

// preparedResult is what runPrepare concluded from a prepare that RAN cleanly: the
// freeform context to fold into the judge's prompt, and whether prepare asked to
// skip the judge outright. A struct rather than a second bool return, matching
// scriptResult's shape — the codebase's idiom for a dispatch step whose result is
// more than one value. It is meaningful only alongside a non-refused Verdict; a
// refusal carries its reason and this is left zero.
type preparedResult struct {
	// Context is prepare's `additionalContext`, folded into the judge input when
	// the judge runs. Zero (nil) when prepare emitted none — or when skipping,
	// where it is moot.
	Context declaration.PreparedContext

	// Skip is prepare's `skip` signal: when true, the judge is not invoked and the
	// check ABSTAINS (reaches no verdict; other checks decide). False is the
	// unchanged "run the judge" default.
	Skip bool

	// Fingerprint is prepare's optional "fingerprint" string.
	Fingerprint string
}

// runPrepare runs a prepare script and returns what it concluded — the
// additionalContext it produced and whether it asked to skip the judge.
//
// prepare receives the SAME CheckPayload a script would on stdin (the spec is
// explicit), so the payload is assembled the same way. Its stdout envelope is read
// for two keys: `additionalContext` (a freeform object, folded into the judge's
// prompt) and `skip` (a typed control signal); everything else it printed is not
// part of the contract. A prepare that could not run, or whose stdout is not the
// `{additionalContext: {...}, skip: <bool>}` shape, fails the check closed: a judge
// fed a half-prepared prompt would judge against something the author did not
// intend, and an envelope the engine cannot read must resolve to a refusal, never a
// silent skip or a half-read prompt.
func (r Runner) runPrepare(req Request, prepare string) (preparedResult, Verdict, error) {
	payload, err := r.checkPayloadJSON(req)
	if err != nil {
		return preparedResult{}, Verdict{}, err
	}
	res, err := r.runScript(scriptCall{
		Dir:            req.Dir,
		Script:         prepare,
		Stdin:          payload,
		GuardName:      req.GuardName,
		Workspace:      req.Workspace,
		SessionID:      req.SessionID,
		TranscriptPath: req.TranscriptPath,
		LaunchedBy:     req.LaunchedBy,
		Env:            req.Env,
	})
	if err != nil {
		return preparedResult{}, Verdict{}, err
	}
	if !res.Passed {
		// prepare exited non-zero. The spec says a prepare failure fails the check;
		// carry its words so the agent hears what prepare complained about.
		return preparedResult{}, refuse(fmt.Sprintf("the judge's prepare step refused (before the model was asked): %s", res.Reason)), nil
	}

	// Read the envelope out of prepare's stdout: `additionalContext` and `skip`,
	// the two keys of the contract; anything else printed is ignored.
	outcome, err := parsePreparedContext(res.Stdout)
	if err != nil {
		return preparedResult{}, refuse(fmt.Sprintf(
			"the judge's prepare step produced output this engine could not read as {\"additionalContext\": {...}, \"skip\": <bool>} (%v); "+
				"refusing rather than asking the model against a half-prepared prompt", err)), nil
	}
	return preparedResult{Context: outcome.AdditionalContext, Skip: outcome.Skip, Fingerprint: outcome.Fingerprint}, pass(), nil
}

// checkPayloadJSON assembles the nature's check payload and marshals it for stdin.
//
// A file-guard's script/prepare receive CheckPayload; a gate's receive
// GateCheckPayload. The two carry the same fields (event, transcriptPath, context)
// and differ only in the doc-level type of `event` — so the assembled JSON is the
// same shape, and the Nature selects which Go type names it for correctness rather
// than changing the bytes. Both are built here so a script's stdin and a judge's
// (prepare-less) input agree.
func (r Runner) checkPayloadJSON(req Request) ([]byte, error) {
	if req.Changeset != nil {
		return json.Marshal(*req.Changeset)
	}
	switch req.Nature {
	case NatureGate:
		return json.Marshal(declaration.GateCheckPayload{
			Event:          declaration.FlatEvent(req.Event),
			TranscriptPath: req.TranscriptPath,
			Context:        req.contextMap(),
		})
	default:
		return json.Marshal(declaration.CheckPayload{
			Event:          declaration.FlatEvent(req.Event),
			TranscriptPath: req.TranscriptPath,
			Context:        req.contextMap(),
		})
	}
}

// judgeInputJSON assembles the nature's judge input — the payload spread flat plus
// `additionalContext` when prepare set one — and marshals it for the template.
//
// FileJudgeInput / GateJudgeInput embed their payload inline, so the payload's own
// fields render unprefixed at the template's top level and `additionalContext`, if
// present, is one more top-level field — never spread into the payload's namespace,
// so a prepare key cannot collide with `event` or `transcriptPath`. That is the
// spec's rule, and inlining the payload struct is what enforces it.
func (r Runner) judgeInputJSON(req Request, additional declaration.PreparedContext) ([]byte, error) {
	if req.Changeset != nil {
		return json.Marshal(changesetJudgeInput{
			Payload:           *req.Changeset,
			Change:            req.Changeset.Changeset.Change(),
			AdditionalContext: additional,
		})
	}
	switch req.Nature {
	case NatureGate:
		return json.Marshal(declaration.GateJudgeInput{
			GateCheckPayload: declaration.GateCheckPayload{
				Event:          declaration.FlatEvent(req.Event),
				TranscriptPath: req.TranscriptPath,
				Context:        req.contextMap(),
			},
			AdditionalContext: additional,
		})
	default:
		return json.Marshal(declaration.FileJudgeInput{
			CheckPayload: declaration.CheckPayload{
				Event:          declaration.FlatEvent(req.Event),
				TranscriptPath: req.TranscriptPath,
				Context:        req.contextMap(),
			},
			Change:            fileChange(req.Event),
			AdditionalContext: additional,
		})
	}
}

// changesetJudgeInput is a changeset file-guard's judge input: the Changeset
// payload spread flat at the template's top level (`{{ changeset }}`,
// `{{ subject }}`, `{{ event }}`, `{{ transcriptPath }}`, `{{ context }}`), plus
// `{{ change }}` — the combined diff of the selected files — and
// `additionalContext` when prepare returned one.
type changesetJudgeInput struct {
	changeset.Payload
	Change            string                      `json:"change"`
	AdditionalContext declaration.PreparedContext `json:"additionalContext,omitempty"`
}

// fileChange is the unified diff of a file event's oldContent to its newContent,
// labelled with its path: a create diffs from nothing, a delete to nothing.
func fileChange(e event.Event) string {
	oldContent, _ := e.Fields["oldContent"].(string)
	newContent, _ := e.Fields["newContent"].(string)
	if oldContent == newContent {
		return ""
	}
	path, _ := e.Fields["path"].(string)
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(oldContent),
		B:        difflib.SplitLines(newContent),
		FromFile: "a/" + path,
		ToFile:   "b/" + path,
		Context:  3,
	})
	if err != nil {
		return ""
	}
	return diff
}

// contextMap returns the request's context map, never nil, so the payload always
// carries `context` as an object rather than a null — the same "always an object"
// discipline the event envelope keeps for a hook that indexes it.
func (req Request) contextMap() map[string]natures.ContextState {
	if req.Context == nil {
		return map[string]natures.ContextState{}
	}
	return req.Context
}

// parsePreparedContext reads the two supported keys off a prepare script's stdout
// envelope: the freeform `additionalContext` and the `skip` control signal.
//
// The wire shape is `{"additionalContext": {...}, "skip": <bool>}`, both keys
// optional. `additionalContext` is the freeform template context; `skip` is a
// separate, typed channel — decoded as a real bool rather than fished out of the
// freeform map, so a context key an author happens to name can never read as a
// skip instruction. Empty stdout is treated as "no additional context, judge
// runs", not an error and NOT a skip: a prepare that ran, decided it had nothing
// to add, and printed nothing is a legitimate no-op (the spec: "exiting without
// producing output leaves ... as it was"), and a no-op still lets the judge run —
// silence is not an abstain. Anything present but not of that shape is an error the
// caller turns into a fail-closed refusal, so a prepare whose output cannot be
// read never accidentally skips the judge (or runs it against a half-read
// envelope) — it refuses.
func parsePreparedContext(stdout []byte) (declaration.PreparedOutcome, error) {
	trimmed := trimSpace(stdout)
	if len(trimmed) == 0 {
		return declaration.PreparedOutcome{}, nil
	}
	var outcome declaration.PreparedOutcome
	if err := json.Unmarshal(trimmed, &outcome); err != nil {
		return declaration.PreparedOutcome{}, err
	}
	return outcome, nil
}

// skillNameOf reads a Skill tool_use's `input.skill`.
//
// The input is kept raw on a ToolCall (what it means is the tool's business), so
// the one field this check needs is decoded here. `skill` and nothing else — the
// spec declares SkillToolInput with that field, and require-skill.sh's own
// measurement settled that no fallback field name occurs.
func skillNameOf(call transcript.ToolCall) string {
	var in struct {
		Skill string `json:"skill"`
	}
	if json.Unmarshal(call.Input, &in) != nil {
		return ""
	}
	return in.Skill
}

// skillNameMatches reports whether a Skill tool_use's own `input.skill` names
// the skill a `require: [{skill: <name>}]` prerequisite declares.
//
// A PROJECT's own skill is invoked bare (`document-decision`); a PLUGIN's is
// invoked plugin-qualified (`sloprail:authoring-guardrails`) — real Claude
// Code prepends the owning plugin's name whenever the skill did not come from
// the project's own `.claude/skills/`, confirmed against real transcripts
// (both forms occur; never the reverse — a project skill is never seen
// prefixed). A declaration names the BARE skill either way — the same name
// SkillFilePaths resolves against both a project's `.claude/skills/<name>/`
// and a plugin's `<pluginRoot>/skills/<name>/` — so this check must accept
// either spelling of the same tool_use, or it can only ever match a
// project-owned skill and refuses every plugin-shipped one's own
// precondition even after the agent genuinely invoked it. Found by a real,
// unscripted agent run (sr-eval): the mock's own Skill() test helper writes
// whatever bare string a test passes it and never generates the qualified
// form, so this gap was invisible to the whole e2e suite.
func skillNameMatches(call transcript.ToolCall, skill string) bool {
	name := skillNameOf(call)
	if name == skill {
		return true
	}
	if _, suffix, found := strings.Cut(name, ":"); found && suffix == skill {
		return true
	}
	return false
}

// trimSpace trims leading/trailing ASCII whitespace from a byte slice without a
// string round-trip, for reading a script's stdout.
func trimSpace(b []byte) []byte {
	start := 0
	for start < len(b) && isSpace(b[start]) {
		start++
	}
	end := len(b)
	for end > start && isSpace(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// CheckRequire evaluates only req.Require, without running any check. A
// changeset evaluation records each prerequisite as a check of its own, so it
// asks one at a time.
func (r Runner) CheckRequire(req Request) (Verdict, error) {
	return r.withDefaults().checkRequire(req)
}

// judgeProject is the project tree a judge reads: the snapshot of the tip being judged
// when the request carries one, else the workspace.
func (req Request) judgeProject() string {
	if req.ProjectRoot != "" {
		return req.ProjectRoot
	}
	return req.Workspace
}
