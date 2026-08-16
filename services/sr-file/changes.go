package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/fingerprint"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/module"
)

// newChangesCmd builds `sr-file changes`, which turns a hook payload into the
// file changes it describes, one JSON object per line.
//
// WHY THIS EXISTS. Every guardrail that reads a file re-implements the same
// twenty lines: pull the tool arguments out of the payload, work out whether
// this is a create or an update, take `content` on one and `result`+`resultKnown`
// on the other, fall back to the disk for a Post kind. Five live rules carry a
// copy, and they are copies of a thing that is genuinely hard — the engine
// resolves shell commands, unwraps interpreters, and reports the paths it
// worked out, which is not something a hook can redo with grep.
//
// So this exposes the module's own answer. What a rule does with the rows is the
// rule's business: `jq` is already a dependency of every hook in this repo and
// already knows how to select, so no matcher language is offered here. One
// parser, many consumers.
//
// WHY IN sr-file. The parsing lives in internal/filemod and this is that
// module's command. `sr-file validate` already answers "does this document
// satisfy a schema"; this answers "what does this action do to which files".
func newChangesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "changes",
		Short: "Report the file changes a hook payload describes, as JSONL",
		Long: "Report the file changes a hook payload describes, one JSON object per line.\n\n" +
			"The payload is read from stdin, as the harness sends it to a hook. Each row carries\n" +
			"the path (relative to the workspace) and the fields the change's kind declares:\n\n" +
			"  {\"kind\":\"PreFileCreate\",\"path\":\"a.md\",\"content\":\"...\",\"markers\":[]}\n" +
			"  {\"kind\":\"PreFileUpdate\",\"path\":\"b.md\",\"result\":\"...\",\"resultKnown\":true,\"markers\":[]}\n" +
			"  {\"kind\":\"PreFileDelete\",\"path\":\"c.md\"}\n\n" +
			"A payload that describes no file change prints nothing and exits 0 — that is the\n" +
			"ordinary case, since most of what happens in a session concerns files not at all.\n\n" +
			"--turn reports what actually differs from HEAD instead — tracked changes, staged or\n" +
			"not, plus untracked files — for a hook with no payload to read (a Stop hook, run at the\n" +
			"end of a turn rather than in response to one tool call). Rows there carry the path and\n" +
			"its current fingerprint, with kind \"Changed\":\n\n" +
			"  {\"kind\":\"Changed\",\"path\":\"a.md\",\"fingerprint\":\"...\"}\n\n" +
			"SELECTING IS THE CALLER'S JOB. There is no matcher here on purpose: the rows are\n" +
			"JSON and jq already does this better than a language invented for it would.\n\n" +
			"  sr-file changes < payload.json | jq -r 'select(.path | test(\"TASK\\\\.md$\")) | .path'\n\n" +
			"EXIT STATUS is 0 whenever the payload was read, whether or not it described any\n" +
			"change. A hook deciding to refuse does so on what it read, not on this status.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          runChanges,
	}
	cmd.Flags().Bool("turn", false, "Report what differs from HEAD instead of reading a payload from stdin")
	return cmd
}

// hookPending adapts a hook payload to filemod.Pending.
//
// A local type rather than a shared one, because the two consumers want
// different things from the same bytes: sr-session reads the whole payload for
// its own session bookkeeping, and this command reads the three fields the
// module asks for. A struct shared between them would be a struct that grows a
// field whenever either side needs one.
type hookPending struct {
	tool string
	args json.RawMessage
	root string
}

func (p hookPending) Tool() string               { return p.tool }
func (p hookPending) Arguments() json.RawMessage { return p.args }
func (p hookPending) Root() string               { return p.root }

// payloadShape is the part of a hook payload this command reads.
//
// The workspace matters as much as the arguments do. filemod reports paths
// RELATIVE to Root, and a rule is written `path startsWith "memories/"` because
// an author cannot know where the repo will be checked out. With no root the
// module reports whatever spelling the harness sent — absolute, for Claude Code
// — and every caller selecting on a relative path silently matches nothing.
type payloadShape struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	Cwd       string          `json:"cwd"`
}

func runChanges(cmd *cobra.Command, _ []string) error {
	turn, _ := cmd.Flags().GetBool("turn")
	if turn {
		return runChangesTurn(cmd)
	}

	data, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return fmt.Errorf("sr-file changes: read stdin: %w", err)
	}

	var p payloadShape
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("sr-file changes: parse payload: %w", err)
	}

	// An absent cwd falls back to the process's own, since a hook runs where the
	// session runs. Reported paths are relative to it either way, so a caller's
	// selector means the same thing in both cases.
	root := p.Cwd
	if root == "" {
		root, _ = os.Getwd()
	}

	// A KNOWN LIMIT, inherited rather than introduced: a command naming a
	// RELATIVE target is only seen when this process is already standing in the
	// workspace.
	//
	// filemod asks the filesystem with the spelling the command used — `rm a.md`
	// — because that is the spelling that resolves; only the REPORTED path is
	// made workspace-relative. Resolution therefore happens against the
	// process's own directory, and a relative target elsewhere reads as absent,
	// producing no event.
	//
	// This is the ENGINE's behaviour, not this command's. Measured on
	// `sr-session pre-tool` with an identical payload and a rule bound to
	// PreFileDelete: run from inside the workspace it refuses `rm a.md`; run
	// from anywhere else it permits, silently, because no event is produced.
	//
	// os.Chdir would paper over it here and was rejected. The working directory
	// is process-wide, this command has no claim on it, and a caller running
	// several payloads — or an engine that ever dispatches modules concurrently
	// — would have the ground move under it. The right fix is in filemod, which
	// already has the root and could resolve against it; it is not made here
	// because it changes what the engine reports and belongs in its own change.
	//
	// What this means for a caller TODAY: absolute paths and tool-driven writes
	// (Write/Edit) are unaffected, since neither depends on the process's
	// directory. A hook that must see relative command targets has to run from
	// the workspace.

	in := module.Input{
		module.InputPhase: module.PhasePre,
		module.InputPayload: hookPending{
			tool: p.ToolName,
			args: p.ToolInput,
			root: root,
		},
	}

	// Events are taken FIRST and the error reported after, per module.Module's
	// contract: a module may return events alongside a non-nil error and a caller
	// must not discard them. `rm a.md b.md` is one call producing two targets, and
	// one path that will not stat must not silence the other — the same defect
	// this engine already fixed once in its own dispatch loop.
	evs, extractErr := filemod.New().Extract(in)
	for _, e := range evs {
		if err := printRow(cmd.OutOrStdout(), e); err != nil {
			return err
		}
	}
	if extractErr != nil {
		// Reported, not fatal: the rows above are real and the caller is acting on
		// them. Exit status stays 0 so a hook is not made to refuse by a problem
		// with one path among several.
		fmt.Fprintf(cmd.ErrOrStderr(), "sr-file changes: %v\n", extractErr)
	}
	return nil
}

// runChangesTurn reports what differs from HEAD, for a hook with no payload —
// a Stop hook, run once at the end of a turn rather than once per tool call.
//
// NOT the same question --pre answers, and not a smaller version of it.
// filemod.Observed needs a session BASELINE — the commit a cycle's difference is
// measured FROM, which only the engine records (sr-session start). A plugin with
// no engine underneath it has no baseline to hand over, so this reads against
// HEAD instead: what is different from the last commit, tracked or not. That is
// a coarser question — "what does not match history" rather than "what did THIS
// session change" — and the honest one to ask without session bookkeeping this
// command has no business owning.
//
// A rule that also wants "have I already judged this exact content" layers
// `sr-file checks skip` on top, which is where that memory actually belongs —
// see checks.go. This command only ever answers what differs; whether the
// difference has been seen before is a separate question with a separate owner.
func runChangesTurn(cmd *cobra.Command) error {
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("sr-file changes --turn: locate working directory: %w", err)
	}
	if ws := os.Getenv("SR_WORKSPACE"); ws != "" {
		root = ws
	}

	changes, err := gitrepo.Changed(root, "HEAD")
	if err != nil {
		return fmt.Errorf("sr-file changes --turn: %w", err)
	}

	for _, c := range changes {
		fp, err := fingerprint.OfFile(root + "/" + c.Path)
		if err != nil {
			// A path git reports as changed and this process cannot read —
			// deleted since the diff was taken, or a permissions problem.
			// Reported per-path rather than aborting the whole report: the
			// rest of what git found is still real.
			fmt.Fprintf(cmd.ErrOrStderr(), "sr-file changes --turn: %s: %v\n", c.Path, err)
			continue
		}
		line, err := json.Marshal(map[string]any{
			"kind":        "Changed",
			"path":        c.Path,
			"fingerprint": fp,
		})
		if err != nil {
			return fmt.Errorf("sr-file changes --turn: encode %s: %w", c.Path, err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(line))
	}
	return nil
}

// printRow writes one event as a single JSON line, with the kind alongside the
// fields it declared.
//
// Flattened rather than nested under an "event" key: the caller's selector is
// almost always about the path, and `jq 'select(.path | test(...))'` reads
// better than `.event.fields.path`. The kind is a sibling of the fields for the
// same reason — a rule that binds create and update alike should not have to
// reach into a second level to tell them apart.
func printRow(w io.Writer, e event.Event) error {
	row := make(map[string]any, len(e.Fields)+1)
	for k, v := range e.Fields {
		row[k] = v
	}
	// Written last so a module that ever declares a field called "kind" cannot
	// displace the event's own — the row's shape is this command's contract, not
	// the module's.
	row["kind"] = e.Kind

	line, err := json.Marshal(row)
	if err != nil {
		return fmt.Errorf("sr-file changes: encode %s: %w", e.Kind, err)
	}
	_, err = fmt.Fprintln(w, string(line))
	return err
}
