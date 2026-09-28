package main

import (
	"bufio"
	"bytes"
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
	"unicode/utf8"

	"github.com/sloprail/sloprail/internal/commandmod"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/grounding"
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
// builtins whose only effect is on stdout, which resolve mode ignores. Not
// printf: `printf -v 'a[$(id)]' x` runs the subscript in bash 4+, and a glob
// or brace word (`?v`, `{-v,x}`) can become that -v without spelling it.
var pureGlue = map[string]bool{"echo": true, "true": true, "false": true, ":": true}

// isSRFileCall accepts the grounded sr-file verbs (direct or via the sr proxy)
// and pure glue.
//
// By bare name only, which resolve mode looks up in siblingPath: `./sr-file`
// or `/tmp/x/sr-file` is whatever program sits there, and running it ahead of
// time would both run it before any rule judged the line and let it write the
// records this hook trusts. The verb is literal too, so only the verbs that
// honour resolve mode ever run in it.
func isSRFileCall(literals []string) bool {
	switch literals[0] {
	case "sr-file":
		return len(literals) > 1 && groundedVerb(literals[1])
	case "sr":
		return len(literals) > 2 && literals[1] == "file" && groundedVerb(literals[2])
	}
	return pureGlue[literals[0]]
}

// srFileCallFor is isSRFileCall that also accepts the program named by a PATH
// (`/opt/sr/bin/sr-file`, `./bin/sr`) when that path, relative to cwd and
// through symbolic links, IS the sr-file or sr installed beside this engine —
// the very binary resolve mode runs, so the dry run and the real run are the
// same program. Any other program named sr-file is whatever sits there, and is
// never run ahead of time.
func srFileCallFor(cwd string) func(literals []string) bool {
	return func(literals []string) bool {
		if isSRFileCall(literals) {
			return true
		}
		name := literals[0]
		if !strings.Contains(name, "/") {
			return false
		}
		base := filepath.Base(name)
		if base != "sr-file" && base != "sr" {
			return false
		}
		if !filepath.IsAbs(name) {
			if cwd == "" {
				return false
			}
			name = filepath.Join(cwd, name)
		}
		if !sameBinary(name, filepath.Join(siblingDir(), base)) {
			return false
		}
		return isSRFileCall(append([]string{base}, literals[1:]...))
	}
}

// siblingDir is the directory this engine's own binary sits in. A variable so
// a test can name another.
var siblingDir = func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}

// sameBinary reports whether a and b are one existing file.
func sameBinary(a, b string) bool {
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	return errA == nil && errB == nil && !ai.IsDir() && os.SameFile(ai, bi)
}

func groundedVerb(v string) bool {
	return v == grounding.VerbWrite || v == grounding.VerbEdit || v == grounding.VerbDelete
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

// citeRecord is the record a pre-tool call's citations resolve from: the
// caller's own where it is on disk, and whether the caller is KNOWN to be a
// sub-agent — its payload says so and Path is its own record — in which case
// Path is never read as a root (transcript.ResolveSubagentCitation).
type citeRecord struct {
	Path     string
	Subagent bool
}

// resolveNotes is what a pure sr-file line's dry run said about the changes it
// could not compute: each failure against its own target (Reportable path), in
// the order the line ran them, and — for a failure sr-file could not tie to a
// target, such as an argument it could not parse — the line's own stderr.
type resolveNotes struct {
	failed []resolveFailure
	line   string
}

type resolveFailure struct{ path, said string }

// For is the note a refusal of the change to path quotes: sr-file's own words
// about THAT file; when the dry run never reached it, which call stopped the
// line (the last to fail — every call after it that did not run was skipped
// on its account) and what it said; or the line's unattributed words when
// sr-file tied its failure to no file.
func (n resolveNotes) For(path string) string {
	var own []string
	for _, f := range n.failed {
		if f.path == path {
			own = append(own, f.said)
		}
	}
	if len(own) > 0 {
		return strings.Join(own, "\n")
	}
	if len(n.failed) > 0 {
		last := n.failed[len(n.failed)-1]
		return fmt.Sprintf("sr-file never computed this change: the line stopped at the sr-file call on %s, which said:\n%s", last.path, last.said)
	}
	return n.line
}

// groundResult is what grounding one pre-tool call yields.
type groundResult struct {
	events []event.Event
	notes  resolveNotes
	// wholes are the paths an sr-file write in the line states in full.
	wholes map[string]bool
}

// groundPreEvents resolves and attaches citations to one pre-tool call's
// events, returning the events to dispatch, the paths an sr-file write states
// in full, and — when a pure sr-file line's dry run said why it could not
// compute a change — sr-file's own words, by the file each is about, so a
// refusal of that uncomputed change can name the cause instead of guessing.
func groundPreEvents(cmd interface{ ErrOrStderr() io.Writer }, p HookPayload, cite citeRecord, events []event.Event) groundResult {
	wholes := map[string]bool{}
	var chain []transcript.Citation
	var notes resolveNotes
	perPath := map[string][]transcript.Citation{}

	var in struct {
		Command string `json:"command"`
	}
	if commandmod.HarnessCommandTools[p.ToolName] && json.Unmarshal(p.ToolInput, &in) == nil && strings.TrimSpace(in.Command) != "" {
		root := p.Root()
		targets := map[string]string{}
		// An sr-file call's citations are keyed by its target as FileTargets
		// resolves it: after the line's `cd`s, and only from words it could read
		// (an unreadable one holds its place), so they land on no path but the
		// call's own.
		for _, t := range commandmod.FileTargets(in.Command) {
			if t.Grounded == nil {
				continue
			}
			key := filemod.Reportable(absFrom(p.Cwd, t.Path), root)
			targets[key] = t.Grounded.Verb
			perPath[key] = append(perPath[key], resolveAll(cite, t.Grounded.Cites)...)
		}
		for _, inv := range commandmod.ExtractCommand(in.Command).Invocations {
			// An invocation's argv DROPS an unreadable word, so `sr-file delete
			// a.md $X` still names a.md here where FileTargets sees two paths. That
			// makes it a target — an unknown result a preventive rule refuses —
			// but never a key for citations, which a shifted argv could misplace.
			if args, ok := grounding.FileArgs(inv.Argv); ok {
				if fc, ok := grounding.TargetOf(args); ok {
					key := filemod.Reportable(absFrom(p.Cwd, fc.Path), root)
					if _, have := targets[key]; !have {
						targets[key] = fc.Verb
					}
				}
			}
			if g, ok, err := grounding.FromArgv(inv.Argv); ok && err == nil && g.File == nil {
				chain = append(chain, resolveAll(cite, g.Cites)...)
			}
		}
		if commandmod.OnlyCalls(in.Command, srFileCallFor(p.Cwd)) {
			records, failed, said, err := runResolve(in.Command, p.Cwd, cite.Path)
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: sr-file resolve:", err)
			} else {
				events = replaceWithResolved(events, records, root, cite, perPath)
				for _, r := range records {
					if r.Verb == grounding.VerbWrite {
						wholes[filemod.Reportable(r.Path, root)] = true
					}
				}
			}
			notes.line = said
			for _, f := range failed {
				notes.failed = append(notes.failed, resolveFailure{path: filemod.Reportable(f.Path, root), said: clip(f.Error, resolveNoteMax)})
			}
		}
		events = ensureFileEvents(events, targets, root)
	}

	var all []transcript.Citation
	all = append(all, chain...)
	for _, k := range sortedKeys(perPath) {
		all = append(all, perPath[k]...)
	}
	for i, e := range events {
		switch e.Kind {
		case commandmod.KindPreInvoke:
			events[i].Fields[grounding.FieldCitations] = grounding.ToWire(dedupe(all))
		case filemod.KindPreCreate, filemod.KindPreUpdate, filemod.KindPreDelete:
			path, _ := e.Fields[filemod.FieldPath].(string)
			cs := dedupe(append(append([]transcript.Citation{}, chain...), perPath[path]...))
			events[i].Fields[grounding.FieldCitations] = grounding.ToWire(cs)
		}
	}
	return groundResult{events: events, notes: notes, wholes: wholes}
}

// resolveAll grounds each request in the session's own records — the user pool
// in the root's, the tool_result pool in the caller's own first and then the
// root's and its sub-agents' (see transcript.ResolveCitation) — keeping those
// that resolve. One that does not is simply not a citation: the requirement
// that one exist is a rule's, and a rule refusing names what is missing.
func resolveAll(cite citeRecord, reqs []transcript.CitationRequest) []transcript.Citation {
	if cite.Path == "" {
		return nil
	}
	resolve := transcript.ResolveCitation
	if cite.Subagent {
		resolve = transcript.ResolveSubagentCitation
	}
	var out []transcript.Citation
	for _, r := range reqs {
		if c, err := resolve(cite.Path, r); err == nil {
			out = append(out, c)
		}
	}
	return out
}

// reground keeps each citation a resolve-mode record reports only if the
// session's own records resolve its quote, in its pools, to the same entry —
// the same line of the same file, since a tool_result may resolve in a
// sub-agent's record rather than the root's.
func reground(cite citeRecord, cs []transcript.Citation) []transcript.Citation {
	var out []transcript.Citation
	for _, c := range cs {
		req := transcript.CitationRequest{Quote: c.Quote, SourceTypes: c.SourceTypes}
		for _, got := range resolveAll(cite, []transcript.CitationRequest{req}) {
			if got.Path == c.Path && got.Line == c.Line {
				out = append(out, got)
			}
		}
	}
	return out
}

// runResolve runs a pure sr-file line in resolve mode and returns what each
// invocation recorded — the changes it computed, and the invocations that
// failed, each against its own target. The records arrive through a directory
// this hook names, never through the line's stdout, which is the command's own
// and may say anything (`sr-file edit ... && echo done`). When the line fails,
// it also returns what it printed to stderr — sr-file's reason, e.g. a quote
// that resolves to no message — trimmed to what a refusal can quote.
func runResolve(line, cwd, transcriptPath string) ([]grounding.Resolved, []grounding.Failed, string, error) {
	dir, err := os.MkdirTemp("", "sr-file-resolve-")
	if err != nil {
		return nil, nil, "", err
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
	var stderr bytes.Buffer
	c.Stdout, c.Stderr = io.Discard, &stderr
	// A failing invocation is an ordinary outcome — the real run fails the
	// same way and changes nothing — so the exit status is not an error here.
	// What it recorded before failing is still what the line would do.
	var said string
	if c.Run() != nil {
		said = clip(strings.TrimSpace(stderr.String()), resolveNoteMax)
	}
	if ctx.Err() != nil {
		return nil, nil, "", fmt.Errorf("timed out after %s", resolveTimeout)
	}

	out, err := readJSONLines[grounding.Resolved](grounding.ResolvedFile(dir))
	if err != nil {
		return nil, nil, said, err
	}
	failed, err := readJSONLines[grounding.Failed](grounding.FailedFile(dir))
	if err != nil {
		return nil, nil, said, err
	}
	return out, failed, said, nil
}

// readJSONLines reads one T per line of the file at path; none when it does not
// exist.
func readJSONLines[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<30)
	for sc.Scan() {
		var r T
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("unreadable record: %w", err)
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

// resolveNoteMax bounds how much of a failed dry run's stderr a refusal quotes.
const resolveNoteMax = 2000

// clip cuts s to at most max bytes on a rune boundary.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// siblingPath is PATH with this binary's own directory first, so resolve mode
// runs the sr-file installed beside the engine judging it.
func siblingPath() string {
	path := os.Getenv("PATH")
	if dir := siblingDir(); dir != "" {
		return dir + string(os.PathListSeparator) + path
	}
	return path
}

// replaceWithResolved swaps the modules' static Pre file events for the paths
// sr-file resolved with exact ones built from its records. A path it did NOT
// resolve — an invocation that would fail, which changes nothing — keeps its
// static event: an unknown result that a preventive rule refuses, never a
// silent pass.
//
// A record's citations are sr-file's say-so, so each is kept only when the
// session's own record grounds its quote on the same line (reground).
func replaceWithResolved(events []event.Event, records []grounding.Resolved, root string, cite citeRecord, perPath map[string][]transcript.Citation) []event.Event {
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
		perPath[key] = reground(cite, ch.cites)
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
