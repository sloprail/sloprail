package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/commandmod"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/module/modules"
	"github.com/sloprail/sloprail/internal/tagmod"
	"github.com/sloprail/sloprail/internal/transcript"
)

// newSessionTrajectoryNormalizeCmd reads a trajectory as normalized entries, each
// enriched with the events re-derived from it.
//
// This is the third question under `trajectory`, and the one that walks the
// entries: `describe` and `cite` are settled by a path and the session's records,
// this reads what happened. It joins two shapes already spec'd rather than
// inventing a third — the normalized `Entry` `query` returns, and the semantic
// events the engine extracts to match guardrails — so a hook asking "did the
// agent run `gh` with which flags" or "was `#refactor` declared" reads the events
// off each entry instead of re-deriving them from the raw record by hand.
//
// It is a READ/emit command: it extracts and prints, and evaluates no matcher and
// runs no guardrail. The events it carries are the same shape a guardrail binds
// to, so a rule reasons about one vocabulary whether an event arrives at a hook or
// comes back here — but here it is jq's to select and reshape, not the engine's to
// act on.
func newSessionTrajectoryNormalizeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "normalize",
		Short: "The trajectory as normalized entries, each carrying the events re-derived from it",
		Long: `Read a trajectory as normalized entries, each enriched with the events
re-derived from it.

Each entry is the normalized Entry (spread whole — type, uuid, isSidechain,
message and the rest) plus two fields: its 1-based physical line in the file,
and the events re-derived FROM it. One assistant entry with three tool calls
yields three events; an entry that yields none carries an empty array.

Only what can be RE-DERIVED from the record populates events — the pre-action
file and command events, and tags (PostTagWrite). Not the git-observed Post file
events, not PreToolUse, not Stop.

  --path <PATH>          which trajectory to read; defaults to the hooked-in one
  --events <Kind,...>    which event kinds populate each entry's events, named by
                         the event's own kind (PreCommandInvoke, PostTagWrite,
                         PreFileCreate, PreFileUpdate, PreFileDelete). Absent
                         means every re-derivable kind; a kind normalize cannot
                         re-derive is refused here.
  --whole-session        read the entire record, not just the part no cycle has
                         judged yet

The answer is NormalizedEntry[] as JSON, for whatever the hook already uses to
read JSON.`,
		Args: cobra.NoArgs,
		RunE: runSessionTrajectoryNormalize,
	}
	cmd.Flags().String("path", "",
		"Which trajectory to read; defaults to the one the hook was invoked for")
	cmd.Flags().StringSlice("events", nil,
		"Comma-separated event kinds to populate each entry's events (default: every re-derivable kind)")
	cmd.Flags().Bool("whole-session", false,
		"Read the entire record, not just the part no cycle has judged yet")
	return cmd
}

func runSessionTrajectoryNormalize(cmd *cobra.Command, _ []string) error {
	// The kinds first, so a bad --events is refused before any trajectory is read
	// or any extractor runs — the spec's "a kind normalize cannot re-derive is
	// refused when the flag is parsed rather than silently returning nothing".
	requested, _ := cmd.Flags().GetStringSlice("events")
	kinds, err := parseEventKinds(requested)
	if err != nil {
		return err
	}

	path, p, err := resolveTrajectory(cmd)
	if err != nil {
		// A path named or guessed and found wanting — a session id that is not a
		// name, a file belonging to another conversation. Reported as itself, the
		// same way describe and cite report it.
		return err
	}
	if path == "" {
		return errNoTrajectory()
	}

	lined, err := transcript.ReadLines(path)
	if err != nil {
		return err
	}

	// The slice, before any extraction. --whole-session reads the whole record;
	// otherwise it reads from where the last completed cycle stopped, the same
	// position `session query` uses — a rule asking what the agent did means this
	// cycle's work, and re-reading judged turns both wastes the reading and lets a
	// judge reach a different verdict on work the agent can no longer reach.
	//
	// The mark is applied over the LINED entries, on their embedded uuids, so the
	// physical line survives the slice — Since narrows by uuid, and the reported
	// line is the one field jq cannot recompute downstream because the stream is a
	// slice and jq's index is relative to it.
	//
	// The mark is consulted only for the HOOKED-IN trajectory — when no --path was
	// given. The read position is a fact about THIS session, kept in this session's
	// own engine state; an explicit --path names another trajectory (a sibling or
	// the parent that describe pointed at) for which the current session has no
	// mark, so there the whole file is the honest read and --whole-session is
	// already its behaviour. This also keeps `readMark`'s "reading the whole
	// session" diagnostic — meant for a session that cannot open its own state —
	// from firing on the ordinary --path case, where the empty payload has no state
	// to open by design.
	wholeSession, _ := cmd.Flags().GetBool("whole-session")
	pathGiven := cmd.Flags().Changed("path")
	if !wholeSession && !pathGiven {
		lined = sinceLined(lined, readMark(cmd, p))
	}

	// The registry drives the extraction: the same modules the hook points run,
	// reached the same way. Producing an event nobody asked for is work done to be
	// discarded — including the whole-transcript work a tag scan would be — so only
	// the modules owning a requested kind are run.
	reg, err := modules.Registry()
	if err != nil {
		return err
	}

	// The workspace file paths are resolved and read relative to. It is the
	// payload's cwd when the hook provided one; empty when a bare --path named
	// another trajectory, in which case the file extractors report paths as the
	// record spelled them and read no tree they cannot reach. Either way the
	// command extractor (a pure function of the command line) is unaffected.
	root := p.Root()

	out := make([]normalizedEntry, 0, len(lined))
	for _, le := range lined {
		out = append(out, normalizedEntry{
			raw:    le.Entry,
			Line:   le.Line,
			Events: deriveEvents(le.Entry, reg, kinds, root),
		})
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}

// deriveEvents re-derives the TrajectoryEvents an entry yields, narrowed to the
// requested kinds.
//
// The derivation runs the SAME extractors the engine runs at a hook, reached
// through the same registry — no command parsing or tag scanning is
// reimplemented here. An entry's tool calls each drive the file and command
// modules (a Pre extraction, one call at a time, exactly as a pre-tool hook feeds
// them a single pending call), and the entry's own prose drives the tag module.
//
// One entry can yield several events — an assistant turn with three tool calls is
// three extractions — and an entry that yields none carries an empty array, which
// is why this always returns a non-nil slice.
func deriveEvents(e transcript.Entry, reg *module.Registry, kinds kindSet, root string) []event.Event {
	events := []event.Event{}

	// The command and file events, one tool call at a time. The modules dispatch
	// on the argument SHAPE, not the tool name, so every call is offered and the
	// ones carrying no command line and no file path simply produce nothing.
	if kinds.wantsAny(commandFileKinds()) {
		mods := reg.Needed(intersect(commandFileKinds(), kinds))
		for _, call := range transcript.ToolCalls(e) {
			in := module.Input{
				module.InputPhase:   module.PhasePre,
				module.InputPayload: pendingCall{name: call.Name, input: call.Input, root: root},
			}
			for _, m := range mods {
				evs, _ := m.Extract(in)
				// The extraction error is deliberately dropped here, unlike at the
				// hook. A file extractor reports the paths it could not stat so a
				// guardrail is not silently skipped — but normalize enforces
				// nothing, and a path that will not resolve simply yields no event
				// for that path, which is the honest "nothing to say about it" this
				// read wants rather than a diagnostic on a command's own stderr.
				events = append(events, keep(evs, kinds)...)
			}
		}
	}

	// The tag event, once for the whole entry: PostTagWrite is a per-message fact,
	// and the entry's assistant prose is what it scans. Only kept when the entry
	// actually carries a tag — an empty PostTagWrite is the cycle-level "the agent
	// wrote no tag" signal a context reacts to, which is not a per-entry event this
	// entry re-derived. An entry with no tag contributes to the empty array, not a
	// PostTagWrite carrying nothing.
	if kinds.has(tagmod.KindPostTagWrite) {
		if text := transcript.AssistantText(e); text != "" {
			if m, ok := reg.Lookup(tagmod.KindPostTagWrite); ok {
				in := module.Input{
					module.InputPhase:    module.PhasePost,
					module.InputMessages: []string{text},
				}
				evs, _ := m.Extract(in)
				for _, ev := range evs {
					if tags, err := tagmod.FromEvent(ev); err == nil && len(tags.Tags) > 0 {
						events = append(events, ev)
					}
				}
			}
		}
	}

	return events
}

// pendingCall is one tool call presented to the file and command modules as the
// action they would extract from — the same module.Pending shape a live hook
// payload satisfies, built here from a recorded tool_use block instead.
//
// It reads what it recognises and nothing more: Tool() and Arguments() are the
// call's own, and Root() is the workspace an absolute path is reported relative
// to. The modules never branch on Tool() (they dispatch on the argument shape),
// so a harness that renamed its shell or write tool is still parsed — the same
// property session_pre_tool relies on.
type pendingCall struct {
	name  string
	input json.RawMessage
	root  string
}

func (c pendingCall) Tool() string               { return c.name }
func (c pendingCall) Arguments() json.RawMessage { return c.input }
func (c pendingCall) Root() string               { return c.root }

// kindSet is the set of event kinds an entry's events are narrowed to.
type kindSet map[string]bool

func (k kindSet) has(kind string) bool { return k[kind] }

// wantsAny reports whether any of the given kinds is in the set — used to skip a
// whole family of extractors when none of its kinds was asked for.
func (k kindSet) wantsAny(kinds []string) bool {
	for _, kind := range kinds {
		if k[kind] {
			return true
		}
	}
	return false
}

// keep filters events to those whose kind is in the set. The extractors may
// produce a kind outside the requested set — a file extractor asked for
// PreFileDelete still runs the same code that could emit PreFileCreate — so the
// narrowing is applied to the events, not only to which modules run.
func keep(events []event.Event, kinds kindSet) []event.Event {
	out := make([]event.Event, 0, len(events))
	for _, e := range events {
		if kinds.has(e.Kind) {
			out = append(out, e)
		}
	}
	return out
}

// intersect returns the kinds in `all` that were requested — the bound set the
// registry is asked which modules serve.
func intersect(all []string, kinds kindSet) []string {
	var out []string
	for _, k := range all {
		if kinds.has(k) {
			out = append(out, k)
		}
	}
	return out
}

// commandFileKinds are the re-derivable kinds that come from a tool call — the
// command event and the pre-action file events. PostTagWrite is not here: it
// comes from the entry's prose, not a tool call, and is driven separately.
func commandFileKinds() []string {
	return []string{
		commandmod.KindPreInvoke,
		filemod.KindPreCreate,
		filemod.KindPreUpdate,
		filemod.KindPreDelete,
	}
}

// trajectoryEventKinds is every kind `normalize` can re-derive — the
// discriminators of the spec's TrajectoryEvent union, and only those. It is the
// default when --events is absent, and the allowlist --events is checked against.
//
// The pre-action file and command events, plus tags. Not the Post file events
// (observed by diffing the tree, not present in the record), not PreToolUse (the
// tool call is the entry itself, already on the raw Entry), not Stop (a harness
// lifecycle event, not a record entry).
func trajectoryEventKinds() []string {
	return append(commandFileKinds(), tagmod.KindPostTagWrite)
}

// parseEventKinds turns the --events flag into the set of kinds to populate, and
// refuses a kind that is not one normalize can re-derive.
//
// Absent means every re-derivable kind. A named kind is validated against the
// TrajectoryEventKind allowlist and refused here — when the flag is parsed —
// rather than silently returning nothing, which is the spec's own reasoning: a
// Post file event, PreToolUse or Stop asked for here is a mistake in the rule,
// and telling the author is better than handing back an empty stream that reads
// as "the agent did none of that".
func parseEventKinds(requested []string) (kindSet, error) {
	all := trajectoryEventKinds()
	if len(requested) == 0 {
		set := make(kindSet, len(all))
		for _, k := range all {
			set[k] = true
		}
		return set, nil
	}

	// The allowlist, and the sorted spelling of it used in every refusal message so
	// the alternatives read in a stable order rather than registration order.
	allowed := make(kindSet, len(all))
	for _, k := range all {
		allowed[k] = true
	}
	sort.Strings(all)

	set := make(kindSet, len(requested))
	for _, r := range requested {
		kind := strings.TrimSpace(r)
		if kind == "" {
			continue
		}
		if !allowed[kind] {
			return nil, fmt.Errorf(
				"sloprail: %q is not an event kind normalize can re-derive — pass one of %s",
				kind, strings.Join(all, ", "))
		}
		set[kind] = true
	}
	if len(set) == 0 {
		// --events was given but held only blanks. That is a request that narrowed
		// to nothing, not an omitted flag, so it is refused rather than widened
		// back to everything — the two mean different things, and folding them
		// would hide a mistake.
		return nil, fmt.Errorf("sloprail: --events named no event kind; pass one of %s",
			strings.Join(all, ", "))
	}
	return set, nil
}

// sinceLined is transcript.Since over lined entries: it narrows to the entries
// after the mark while keeping each entry's physical line.
//
// transcript.Since works on the entries' uuids, which the lined entries carry, so
// this walks for the mark and slices — the same "an empty or unfindable mark
// yields everything" semantics, which is the safe over-read a lost position
// wants. Kept here rather than added to transcript so the line-bearing type does
// not force a second Since into that package's API.
func sinceLined(entries []transcript.LinedEntry, mark string) []transcript.LinedEntry {
	if mark == "" {
		return entries
	}
	for i, e := range entries {
		if e.UUID == mark {
			return entries[i+1:]
		}
	}
	return entries
}

// normalizedEntry is one entry as `normalize` returns it: the normalized Entry
// spread whole, plus its physical line and the events re-derived from it.
//
// Entry is SPREAD rather than nested, so the output is a clean superset — a
// consumer already reading raw entries reads these the same way, with two fields
// more. That is why this marshals by hand: the entry's own fields sit at the top
// level beside `line` and `events`, which a struct embedding cannot express once
// the entry is carried as its typed form (encoding/json flattens an embedded
// struct, but only the entry's declared fields, and the merge here keeps the two
// added fields from ever colliding with one the entry already has).
type normalizedEntry struct {
	raw    transcript.Entry
	Line   int
	Events []event.Event
}

// MarshalJSON writes the entry's fields spread at the top level, with `line` and
// `events` added.
//
// The entry is marshaled to an object and its keys copied out, then `line` and
// `events` are set. `events` is always an array — never null — because an entry
// that yielded none still carries the empty one the spec promises, and each event
// marshals FLAT (declaration.FlatEvent) — `kind` beside the event's own fields,
// the shape a guardrail check reads under `.event` — so a script reads a past
// event exactly as it reads the live one: `.invocations`, never `.fields.invocations`.
func (n normalizedEntry) MarshalJSON() ([]byte, error) {
	entryJSON, err := json.Marshal(n.raw)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(entryJSON, &fields); err != nil {
		return nil, err
	}

	line, err := json.Marshal(n.Line)
	if err != nil {
		return nil, err
	}
	fields["line"] = line

	events := make([]declaration.FlatEvent, 0, len(n.Events))
	for _, e := range n.Events {
		events = append(events, declaration.FlatEvent(e))
	}
	evs, err := json.Marshal(events)
	if err != nil {
		return nil, err
	}
	fields["events"] = evs

	return json.Marshal(fields)
}
