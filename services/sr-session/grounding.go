package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sloprail/sloprail/internal/commandmod"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/grounding"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// Grounding: attaching `citations` to the events a Bash call produces.
//
// Modules report what a command line SAYS; which citations ground it means
// reading the session's record, which is this service's business. So after the
// modules extract a pre-tool call's events, groundPreEvents does two things:
//
//  1. RESOLVE a line made only of sr-file invocations by RUNNING it in resolve
//     mode (grounding.EnvResolveDir): the binary that will make the change
//     reports it, after the real shell's quoting and expansion, and its file
//     events replace the modules' static predictions — exact bytes, and the
//     citations sr-file itself resolved. A line that could do anything else is
//     never run (commandmod.OnlyCalls).
//
//  2. ATTACH citations. A `sr-session trajectory cite '<quote>' && <cmd>` chain
//     grounds the whole call, so its citation lands on every event the call
//     produced. An sr-file --cite: flag grounds that invocation's own file, so it
//     lands on that file's events and on the command event.
//
// sloprail's job stops at EXISTENCE: a citation here resolved to a real entry
// of the session's record in the named pool. Whether it actually grounds the
// change is for the rule's judge, which reads event.citations.

// resolveTimeout bounds a resolve-mode run. sr-file computes a string replace
// and a citation lookup; anything near this is a wedged process, not work.
const resolveTimeout = 20 * time.Second

// pureGlue are the programs a resolve-mode line may run besides sr-file:
// builtins whose only effect is on stdout, which resolve mode ignores.
var pureGlue = map[string]bool{"echo": true, "printf": true, "true": true, "false": true, ":": true}

// isSRFileCall accepts sr-file (direct or via the sr proxy) and pure glue.
func isSRFileCall(literals []string) bool {
	switch filepath.Base(literals[0]) {
	case "sr-file":
		return true
	case "sr":
		return len(literals) > 1 && literals[1] == "file"
	}
	return pureGlue[literals[0]]
}

// requiresCitation reports whether any loaded rule requires a citation.
func requiresCitation(loaded declaration.Loaded) bool {
	var reqs [][]declaration.Prerequisite
	for _, g := range loaded.FileGuards {
		reqs = append(reqs, g.Require)
	}
	for _, g := range loaded.Gates {
		reqs = append(reqs, g.Require)
	}
	for _, c := range loaded.Contexts {
		reqs = append(reqs, c.Require)
	}
	for _, list := range reqs {
		for _, r := range list {
			if r.Citation != nil {
				return true
			}
		}
	}
	return false
}

// groundPreEvents resolves and attaches citations to one pre-tool call's
// events, returning the events to dispatch and the citations each touched file
// path was grounded in (for recordCitations).
func groundPreEvents(cmd interface{ ErrOrStderr() io.Writer }, p HookPayload, transcriptPath string, events []event.Event) ([]event.Event, map[string][]transcript.Citation) {
	var chain []transcript.Citation
	perPath := map[string][]transcript.Citation{}

	var in struct {
		Command string `json:"command"`
	}
	if commandmod.HarnessCommandTools[p.ToolName] && json.Unmarshal(p.ToolInput, &in) == nil && strings.TrimSpace(in.Command) != "" {
		root := p.Root()
		targets := map[string]string{}
		for _, inv := range commandmod.ExtractCommand(in.Command).Invocations {
			if args, ok := grounding.FileArgs(inv.Argv); ok {
				if verb, path, ok := grounding.TargetOf(args); ok {
					targets[filemod.Reportable(absFrom(p.Cwd, path), root)] = verb
				}
			}
			g, ok, err := grounding.FromArgv(inv.Argv)
			if !ok || err != nil {
				continue
			}
			cites := resolveAll(transcriptPath, g.Cites)
			if g.File == nil {
				chain = append(chain, cites...)
				continue
			}
			key := filemod.Reportable(absFrom(p.Cwd, g.File.Path), root)
			perPath[key] = append(perPath[key], cites...)
		}
		if commandmod.OnlyCalls(in.Command, isSRFileCall) {
			if records, err := runResolve(in.Command, p.Cwd, transcriptPath); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: sr-file resolve:", err)
			} else {
				events = replaceWithResolved(events, records, root, perPath)
			}
		}
		events = ensureFileEvents(events, targets, root)
	}

	var all []transcript.Citation
	all = append(all, chain...)
	for _, k := range sortedKeys(perPath) {
		all = append(all, perPath[k]...)
	}
	grounded := map[string][]transcript.Citation{}
	for i, e := range events {
		switch e.Kind {
		case commandmod.KindPreInvoke:
			events[i].Fields[grounding.FieldCitations] = grounding.ToWire(dedupe(all))
		case filemod.KindPreCreate, filemod.KindPreUpdate, filemod.KindPreDelete:
			path, _ := e.Fields[filemod.FieldPath].(string)
			cs := dedupe(append(append([]transcript.Citation{}, chain...), perPath[path]...))
			events[i].Fields[grounding.FieldCitations] = grounding.ToWire(cs)
			grounded[path] = cs
		}
	}
	return events, grounded
}

// resolveAll grounds each request in the session's own record, keeping those
// that resolve. One that does not is simply not a citation: the requirement
// that one exist is a rule's, and a rule refusing names what is missing.
func resolveAll(transcriptPath string, reqs []transcript.CitationRequest) []transcript.Citation {
	if transcriptPath == "" {
		return nil
	}
	var out []transcript.Citation
	for _, r := range reqs {
		if c, err := transcript.ResolveCitation(transcriptPath, r); err == nil {
			out = append(out, c)
		}
	}
	return out
}

// runResolve runs a pure sr-file line in resolve mode and returns what each
// invocation recorded. The records arrive through a directory this hook names,
// never through the line's stdout, which is the command's own and may say
// anything (`sr-file edit ... && echo done`).
func runResolve(line, cwd, transcriptPath string) ([]grounding.Resolved, error) {
	dir, err := os.MkdirTemp("", "sr-file-resolve-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	ctx, cancel := context.WithTimeout(context.Background(), resolveTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "bash", "-c", line)
	c.Dir = cwd
	c.Env = append(os.Environ(),
		grounding.EnvResolveDir+"="+dir,
		grounding.EnvTranscript+"="+transcriptPath,
		"PATH="+siblingPath(),
	)
	c.Stdout, c.Stderr = io.Discard, io.Discard
	// A failing invocation is an ordinary outcome — the real run fails the
	// same way and changes nothing — so the exit status is not an error here.
	// What it recorded before failing is still what the line would do.
	_ = c.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("timed out after %s", resolveTimeout)
	}

	f, err := os.Open(grounding.ResolvedFile(dir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []grounding.Resolved
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<30)
	for sc.Scan() {
		var r grounding.Resolved
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("unreadable record: %w", err)
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

// siblingPath is PATH with this binary's own directory first, so resolve mode
// runs the sr-file installed beside the engine judging it.
func siblingPath() string {
	path := os.Getenv("PATH")
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe) + string(os.PathListSeparator) + path
	}
	return path
}

// replaceWithResolved swaps the modules' static Pre file events for the paths
// sr-file resolved with exact ones built from its records. A path it did NOT
// resolve — an invocation that would fail, which changes nothing — keeps its
// static event: an unknown result that a preventive rule refuses, never a
// silent pass.
func replaceWithResolved(events []event.Event, records []grounding.Resolved, root string, perPath map[string][]transcript.Citation) []event.Event {
	type change struct {
		first, last grounding.Resolved
		cites       []transcript.Citation
	}
	changes := map[string]*change{}
	var order []string
	for _, r := range records {
		key := filemod.Reportable(r.Path, root)
		ch, ok := changes[key]
		if !ok {
			ch = &change{first: r}
			changes[key] = ch
			order = append(order, key)
		}
		ch.last = r
		ch.cites = append(ch.cites, r.Citations...)
	}
	if len(changes) == 0 {
		return events
	}

	var out []event.Event
	for _, e := range events {
		switch e.Kind {
		case filemod.KindPreCreate, filemod.KindPreUpdate, filemod.KindPreDelete:
			if path, _ := e.Fields[filemod.FieldPath].(string); changes[path] != nil {
				continue
			}
		}
		out = append(out, e)
	}
	for _, key := range order {
		ch := changes[key]
		perPath[key] = ch.cites
		existed := ch.first.Existed
		fe := filemod.FileEvent{Path: key}
		if existed {
			fe.OldContent = ch.first.OldContent
			fe.OldMarkers = filemod.Scan(fe.OldContent)
		}
		switch {
		case ch.last.Verb == grounding.VerbDelete:
			if !existed {
				continue
			}
			out = append(out, fe.Event(filemod.KindPreDelete))
		default:
			fe.NewContent = ch.last.NewContent
			fe.NewMarkers = filemod.Scan(fe.NewContent)
			fe.ResultKnown = true
			kind := filemod.KindPreUpdate
			if !existed {
				kind = filemod.KindPreCreate
			}
			out = append(out, fe.Event(kind))
		}
	}
	return out
}

// ensureFileEvents gives every sr-file target that has no Pre file event yet one
// whose result is UNKNOWN. The file module declines to predict a creation whose
// bytes it cannot state, so without this an sr-file write that was not resolved
// — a line mixing it with other programs, or one whose resolve failed — would
// reach no rule at pre-tool and land unjudged. With an unknown result, a
// preventive rule refuses it instead.
func ensureFileEvents(events []event.Event, targets map[string]string, root string) []event.Event {
	have := map[string]bool{}
	for _, e := range events {
		switch e.Kind {
		case filemod.KindPreCreate, filemod.KindPreUpdate, filemod.KindPreDelete:
			path, _ := e.Fields[filemod.FieldPath].(string)
			have[path] = true
		}
	}
	keys := make([]string, 0, len(targets))
	for k := range targets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if have[key] {
			continue
		}
		abs := key
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, key)
		}
		fe := filemod.FileEvent{Path: key}
		b, err := os.ReadFile(abs)
		exists := err == nil
		if exists {
			fe.OldContent = string(b)
			fe.OldMarkers = filemod.Scan(fe.OldContent)
		}
		switch {
		case targets[key] == grounding.VerbDelete:
			if exists {
				events = append(events, fe.Event(filemod.KindPreDelete))
			}
		case exists:
			events = append(events, fe.Event(filemod.KindPreUpdate))
		default:
			events = append(events, fe.Event(filemod.KindPreCreate))
		}
	}
	return events
}

// recordCitations remembers, per file path, the citations a permitted pre-tool
// call grounded its change in, so the Post events at Stop — built from the tree
// difference, which knows nothing of commands — carry them too.
//
// Cited changes to one file accumulate. An UNCITED change clears the file's
// record: the file now holds a change nothing grounds, and citations recorded
// for an earlier change must not vouch for it.
func recordCitations(store sessionstate.Store, grounded map[string][]transcript.Citation) error {
	if store == nil || len(grounded) == 0 {
		return nil
	}
	for attempt := 0; attempt < 5; attempt++ {
		old, _, err := store.Meta(sessionstate.MetaCitations)
		if err != nil {
			return err
		}
		all := map[string][]transcript.Citation{}
		if old != "" {
			_ = json.Unmarshal([]byte(old), &all)
		}
		for path, cs := range grounded {
			if len(cs) == 0 {
				delete(all, path)
				continue
			}
			all[path] = dedupe(append(all[path], cs...))
		}
		raw, err := json.Marshal(all)
		if err != nil {
			return err
		}
		ok, err := store.SwapMeta(sessionstate.MetaCitations, old, string(raw))
		if err != nil || ok {
			return err
		}
	}
	return fmt.Errorf("citations not recorded: the record kept changing underneath")
}

// attachRecordedCitations sets `citations` on each Post file event to what the
// session recorded for its path.
func attachRecordedCitations(store sessionstate.Store, events []event.Event) {
	if store == nil {
		return
	}
	raw, ok, err := store.Meta(sessionstate.MetaCitations)
	if err != nil || !ok || raw == "" {
		return
	}
	all := map[string][]transcript.Citation{}
	if json.Unmarshal([]byte(raw), &all) != nil {
		return
	}
	for i, e := range events {
		path, _ := e.Fields[filemod.FieldPath].(string)
		if cs := all[path]; len(cs) > 0 {
			events[i].Fields[grounding.FieldCitations] = grounding.ToWire(cs)
		}
	}
}

func absFrom(cwd, path string) string {
	if filepath.IsAbs(path) || cwd == "" {
		return path
	}
	return filepath.Join(cwd, path)
}

func dedupe(cs []transcript.Citation) []transcript.Citation {
	seen := map[string]bool{}
	var out []transcript.Citation
	for _, c := range cs {
		k := fmt.Sprintf("%s\x00%d\x00%s", c.Path, c.Line, c.Quote)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, c)
	}
	return out
}

func sortedKeys(m map[string][]transcript.Citation) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
