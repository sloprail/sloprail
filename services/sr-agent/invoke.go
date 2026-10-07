package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ErrBadHarnessArgs is returned when a `--<harness>-args` value is not a JSON
// object of scalars.
var ErrBadHarnessArgs = errors.New("invalid harness arguments")

// ErrWrongHarnessArgs is returned when one harness's args flag is given while a
// different harness is running.
var ErrWrongHarnessArgs = errors.New("harness arguments for the wrong harness")

// ParseHarnessArgs turns a `--claude-args` JSON object into flag/value pairs.
//
// The value is decoded as JSON rather than scanned for a key pattern. That is
// not fussiness: a previous command in this repo read JSON by looking for the
// literal `"key":"` and worked only because Go's encoder emits exactly that
// spacing — every hand-written object, and every object from any other
// producer, would have silently yielded nothing. A real decoder accepts
// whatever is valid JSON, which is the only thing the flag promises.
//
// Values are decoded into `any` so that numbers and booleans are accepted
// alongside strings — a caller writing `{"max-budget-usd": 5}` wrote valid JSON
// and meant something obvious. They are rendered back to strings for the
// command line, since that is what an argv holds. Nested objects and arrays are
// refused: there is no unambiguous way to render one as a flag value, and
// guessing would pass the harness something the author did not write.
//
// Keys are returned sorted, so one input always produces one argv. An argv that
// varied run to run would make the command unreproducible and its tests
// order-dependent.
func ParseHarnessArgs(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	// json.Number keeps the literal the author wrote. Decoding into float64
	// would turn `5` into `5` but `1e3` into `1000`, and a large integer into
	// scientific notation — passing the harness a number the author never
	// typed.
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()

	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, fmt.Errorf(
			"%w: expected a JSON object like '{\"permission-mode\":\"plan\"}', got %s",
			ErrBadHarnessArgs, err)
	}
	// A trailing token means the value was more than one JSON document —
	// `{} {}` decodes the first and would silently drop the rest.
	if dec.More() {
		return nil, fmt.Errorf(
			"%w: expected a single JSON object, got more than one value", ErrBadHarnessArgs)
	}
	if obj == nil {
		return nil, fmt.Errorf(
			"%w: expected a JSON object, got null", ErrBadHarnessArgs)
	}

	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	args := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		if key == "" {
			return nil, fmt.Errorf("%w: empty key", ErrBadHarnessArgs)
		}
		value, err := scalarString(obj[key])
		if err != nil {
			return nil, fmt.Errorf("%w: key %q: %s", ErrBadHarnessArgs, key, err)
		}
		args = append(args, "--"+key, value)
	}
	return args, nil
}

// scalarString renders one JSON scalar as a command-line value.
func scalarString(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	case bool:
		return strconv.FormatBool(v), nil
	case nil:
		return "", errors.New("null is not a value a flag can carry")
	default:
		return "", fmt.Errorf(
			"only strings, numbers and booleans can be flag values, got %T", value)
	}
}

// ErrBadAllowedTools is returned when an --allowed-tools value has a rule whose
// parentheses do not balance.
var ErrBadAllowedTools = errors.New("invalid --allowed-tools")

// ParseAllowedTools splits the --allowed-tools value into individual tool rules.
//
// The flag takes the SAME comma-or-space-separated form claude's own
// `--allowed-tools <tools...>` documents, so an author who knows one knows this.
// Both separators are honoured and empty fields dropped, so "Read, WebFetch" and
// "Read WebFetch" and "Read,WebFetch" all yield the same two tools and a stray
// comma grants nothing rather than an empty tool name.
//
// A separator INSIDE parentheses is part of the rule, not a split: a scoped rule
// like `Bash(git show:*)` or `Bash(curl -sL:*)` is one rule, as claude itself
// reads it (measured on claude 2.1.282: `--allowed-tools "Write Bash(curl
// -sL:*)"` as one argument granted both). Splitting at every space turned it into
// `Bash(git` and `show:*)`, which is harmless only while the pieces are joined
// back into one argument; passed as separate argv values — which the grant does,
// so that a path with a space stays one rule — the second piece begins with a
// dash or means nothing, and the rule the author wrote is lost. A value whose
// parentheses do not balance is refused rather than guessed at.
//
// Measured end to end on 2026-09-27 (claude 2.1.282, haiku, `sr-agent --verify
// --allowed-tools "Bash(curl -sL:*) Bash(git show:*)"`): the rules reached
// claude as two whole values, `curl -sL <url> | head -3` ran, and nothing
// broader did — `curl -s <url>` (no -L), `touch`, `git commit` and `git -C
// <dir> show` (which is not the prefix `git show`) were all refused.
//
// A tool name is not otherwise validated here: like a concrete model name in a
// model set, whether the harness HAS a tool by that name is the harness's to
// answer, not this binary's — sr-agent's job is to pass the request through in the
// harness's own spelling.
func ParseAllowedTools(raw string) ([]string, error) {
	var (
		tools []string
		cur   strings.Builder
		depth int
	)
	flush := func() {
		if cur.Len() > 0 {
			tools = append(tools, cur.String())
			cur.Reset()
		}
	}
	for _, r := range raw {
		switch {
		case r == '(':
			depth++
			cur.WriteRune(r)
		case r == ')':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("%w: %q has a ')' with no '(' before it", ErrBadAllowedTools, raw)
			}
			cur.WriteRune(r)
		case depth == 0 && (r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'):
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("%w: %q has a '(' that is never closed", ErrBadAllowedTools, raw)
	}
	flush()
	// The same shape the loader checks (internal/declaration validateToolRule):
	// a name, then at most one scope that closes at the rule's end. `()` or
	// `(x)` names no tool, and `Bash(x)y` trails text after its scope.
	for _, t := range tools {
		name, scope, scoped := strings.Cut(t, "(")
		if name == "" {
			return nil, fmt.Errorf("%w: %q names no tool before its '('", ErrBadAllowedTools, t)
		}
		if scoped && !strings.HasSuffix(scope, ")") {
			return nil, fmt.Errorf("%w: %q has text after its closing ')'", ErrBadAllowedTools, t)
		}
	}
	return tools, nil
}

// ErrBadAddDir is returned when an `--add-dir[:<mode>]` names no existing
// directory, or names one in two modes at once.
var ErrBadAddDir = errors.New("invalid --add-dir")

// ErrUnknownAddDirMode is returned for an `--add-dir:<mode>` whose mode is not
// one of addDirModes.
var ErrUnknownAddDirMode = errors.New("unknown --add-dir mode")

// ErrModeUnsupported is returned when a harness is asked for an --add-dir mode
// its permission model cannot express.
var ErrModeUnsupported = errors.New("harness cannot honour an --add-dir mode")

// addDirFlag is the flag a caller gives a directory to the agent with. It
// mirrors claude's own --add-dir, and takes a MODE the way sr-file's
// `--cite:<source-types>` takes its pools: `--add-dir <path>` is readable and
// writable, `--add-dir:readonly <path>` readable only. One flag spelling per
// mode keeps the mode on the flag, where a reader of the command line sees it,
// rather than in a second flag that would have to be paired with the right
// path.
const addDirFlag = "add-dir"

// addDirModes is every mode `--add-dir:<mode>` accepts, by suffix; "" is the
// bare `--add-dir`. Each entry becomes its own repeatable flag (see newRoot),
// so the flag parser gives both of sr-file's affordances for free: the value
// as the next word or after '=', and '--' ending the flags.
var addDirModes = []struct {
	suffix string
	mode   dirMode
	usage  string
}{
	{"", dirWritable, "A directory the agent may read and write, as claude's own --add-dir (repeatable)"},
	{"readonly", dirReadonly, "A directory the agent may read but never write, e.g. the project a judge is judging (repeatable)"},
}

// addDirFlagName is the flag spelling for a mode suffix: `add-dir` or
// `add-dir:<suffix>`.
func addDirFlagName(suffix string) string {
	if suffix == "" {
		return addDirFlag
	}
	return addDirFlag + ":" + suffix
}

// addDirModeNames lists the accepted spellings, for a refusal to name.
func addDirModeNames() string {
	names := make([]string, 0, len(addDirModes))
	for _, m := range addDirModes {
		names = append(names, "--"+addDirFlagName(m.suffix))
	}
	return strings.Join(names, ", ")
}

// unknownAddDirMode recognises the flag parser's "unknown flag: --add-dir:<x>"
// and turns it into a refusal naming the modes that exist. Without it a typo'd
// mode reads as an unrelated unknown flag, and the caller is left to guess that
// the mode — not the flag — was the problem.
func unknownAddDirMode(err error) error {
	const prefix = "unknown flag: --" + addDirFlag + ":"
	msg := err.Error()
	if !strings.HasPrefix(msg, prefix) {
		return nil
	}
	return fmt.Errorf("%w %q in %s: the modes are %s",
		ErrUnknownAddDirMode, strings.TrimPrefix(msg, prefix), strings.TrimPrefix(msg, "unknown flag: "), addDirModeNames())
}

// resolveAddDirs turns the given `--add-dir[:<mode>]` values into grants: each
// path made absolute and checked to be a directory. Grants come out writable
// dirs first, then readonly ones, each mode in the order its flags were given.
//
// Absolute because a harness permission rule is matched on an absolute path — a
// relative one would be read against whatever the harness takes as its base,
// which is not this process's cwd. Checked because a missing directory would not
// fail loudly anywhere: the agent would simply be denied every read in it, and a
// judge would reach its verdict blind.
//
// Refused, rather than granted in a form that does not mean what it says:
//   - a directory named in TWO modes: "writable" and "never writable" cannot
//     both be true of it. Compared by resolved path, so /var/x and
//     /private/var/x (the same dir on macOS) are one directory, not two;
//   - a writable dir INSIDE a readonly one: the readonly dir's Edit deny beats
//     every allow, and claude has no "except this sub-dir" form, so the nested
//     dir could never actually be written — the caller would get a grant that
//     silently does nothing. (A readonly dir inside a writable one is fine:
//     its deny wins, which is what it asks for.);
//   - a path with a glob character in it (see unsafeRulePath).
func resolveAddDirs(byMode map[dirMode][]string) ([]dirGrant, error) {
	var grants []dirGrant
	modeOf := map[string]dirMode{}
	for _, m := range addDirModes {
		for _, entry := range byMode[m.mode] {
			flag := "--" + addDirFlagName(m.suffix)
			if strings.TrimSpace(entry) == "" {
				return nil, fmt.Errorf("%w: %s was given an empty path", ErrBadAddDir, flag)
			}
			abs, err := filepath.Abs(entry)
			if err != nil {
				return nil, fmt.Errorf("%w: %s %q: %s", ErrBadAddDir, flag, entry, err)
			}
			info, err := os.Stat(abs)
			if err != nil {
				return nil, fmt.Errorf("%w: %s %q: %s", ErrBadAddDir, flag, entry, err)
			}
			if !info.IsDir() {
				return nil, fmt.Errorf("%w: %s %q is not a directory", ErrBadAddDir, flag, entry)
			}
			if bad := unsafeRulePath(abs); bad != "" {
				return nil, fmt.Errorf("%w: %s %q: %s", ErrBadAddDir, flag, entry, bad)
			}
			key := resolvedPath(abs)
			if prev, seen := modeOf[key]; seen {
				if prev != m.mode {
					return nil, fmt.Errorf("%w: %s is given both writable and readonly; name it once, in the mode you mean",
						ErrBadAddDir, abs)
				}
				continue
			}
			modeOf[key] = m.mode
			grants = append(grants, dirGrant{Path: abs, Mode: m.mode})
		}
	}
	for _, w := range grants {
		if w.Mode != dirWritable {
			continue
		}
		for _, r := range grants {
			if r.Mode == dirReadonly && within(w.Path, r.Path) {
				return nil, fmt.Errorf("%w: --add-dir %s lies inside --add-dir:readonly %s; a readonly dir's deny covers everything under it and cannot make an exception, so the nested dir could never be written. Add a writable dir outside it",
					ErrBadAddDir, w.Path, r.Path)
			}
		}
	}
	return grants, nil
}

// resolvedPath is path with its symlinks resolved, or cleaned when it does not
// resolve: the form two spellings of one directory agree on.
func resolvedPath(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return filepath.Clean(path)
}

// ruleGlobChars are the characters a Claude Code path rule reads as a glob
// rather than as a literal.
const ruleGlobChars = "*?[]{}\\"

// unsafeRulePath reports why a directory cannot be granted as a path rule, or ""
// when it can. A path is turned into `Edit(//<path>/**)`, and claude reads that
// path as a GLOB: a project named `p[1]` becomes a character class that does not
// match the directory itself, so the readonly deny covered nothing (measured in
// review: writes landed in the project with Write granted, and with no tools at
// all under a user's acceptEdits). No escaping of these characters was proven to
// work against the real CLI, so such a path is refused — fail closed — in both
// its given and its resolved spelling, since a rule is emitted for each.
// Re-measured through sr-agent (2026-09-27, claude 2.1.282, haiku): a project
// at `.../p[1]` is refused before claude starts; a project at `.../my proj (1)`
// — spaces and parentheses are not glob characters — is granted, and its deny
// held (Write into it refused, Read of it allowed).
func unsafeRulePath(path string) string {
	for _, form := range []string{path, resolvedPath(path)} {
		if i := strings.IndexAny(form, ruleGlobChars); i >= 0 {
			return fmt.Sprintf("the path contains %q, which a permission rule reads as a glob character, so a rule for it would not match the directory. Rename or move the directory", form[i])
		}
	}
	return ""
}

// harnessGrant turns the access a run needs into the harness's own flags, via
// the harness's grant.
//
// A harness with no grant still gets the caller's tools, as its own
// `--allowed-tools`, and writable dirs cost it nothing — with no permission
// model everything is already writable. A READONLY dir is refused there rather
// than dropped or passed as a plain directory: "read but never write" is a
// promise about what the agent cannot do, and a harness that cannot express it
// would either leave the judge blind or hand it write access, and the caller
// asked for neither.
func harnessGrant(spec harnessSpec, g accessGrant) ([]string, error) {
	if spec.grant != nil {
		return spec.grant(g), nil
	}
	if spec.grantEnv != nil {
		return nil, nil // expressed through the environment: see harnessGrantEnv
	}
	if len(g.DenyTools) > 0 {
		return nil, fmt.Errorf("%w: %s has no permission model to deny tools in; drop --disallowed-tools or run a harness that has one",
			ErrModeUnsupported, spec.name)
	}
	for _, d := range g.Dirs {
		if d.Mode == dirReadonly {
			return nil, fmt.Errorf("%w: %s has no permission model to express --%s in; drop it or run a harness that has one",
				ErrModeUnsupported, spec.name, addDirFlagName("readonly"))
		}
	}
	if len(g.Tools) > 0 {
		return []string{"--allowed-tools", strings.Join(g.Tools, " ")}, nil
	}
	return nil, nil
}

// harnessGrantEnv is harnessGrant for a harness that takes its permissions from a
// configuration it reads (harnessSpec.grantEnv). Its error is what harnessGrant
// would have refused. cleanup is never nil.
func harnessGrantEnv(spec harnessSpec, g accessGrant) (env, args []string, cleanup func(), err error) {
	if spec.grantEnv == nil {
		return nil, nil, func() {}, nil
	}
	env, args, cleanup, err = spec.grantEnv(g)
	if cleanup == nil {
		cleanup = func() {}
	}
	return env, args, cleanup, err
}

// CheckHarnessArgs reports a harness-args flag given while a different harness
// is running.
//
// This is an ERROR rather than something ignored. The author passed settings
// they expected to take effect; dropping them silently means the agent runs
// configured differently than the command says it is, and nothing anywhere
// says so. Naming both harnesses tells them exactly what happened.
func CheckHarnessArgs(flagName string, forHarness Harness, running harnessSpec) error {
	if forHarness == running.name {
		return nil
	}
	return fmt.Errorf(
		"%w: %s applies to %s, but %s is running. "+
			"Those settings would not take effect; remove the flag or pass --harness %s",
		ErrWrongHarnessArgs, flagName, forHarness, running.name, forHarness)
}

// Invocation is the command line sr-agent will run.
type Invocation struct {
	// Binary is the harness executable.
	Binary string

	// Args is its full argument list, prompt included unless Stdin carries it.
	Args []string

	// Stdin, when non-empty, is the prompt, fed on the harness's standard input
	// because it is too large to be an argument (see harnessSpec.stdinPromptAbove).
	Stdin string

	// Env is added to the environment the harness runs in, after the sanitized
	// parent's: what a harness reads its per-run permissions from.
	Env []string
}

// String renders the invocation for diagnostics. Arguments containing spaces
// or shell metacharacters are quoted so a printed command can be pasted back
// into a shell and mean the same thing — a prompt is almost always such an
// argument, and a permission rule like `Edit(//dir/**)` is another: unquoted,
// its parentheses are a shell syntax error and its `**` a glob.
//
// An argument holding `$` or a backtick is SINGLE-quoted: inside strconv.Quote's
// double quotes a shell still expands `$VAR` and `$(cmd)`, so a pasted command
// would run something the argv never held.
func (inv Invocation) String() string {
	parts := make([]string, 0, len(inv.Args)+1)
	parts = append(parts, inv.Binary)
	for _, arg := range inv.Args {
		if strings.ContainsAny(arg, "$`") {
			parts = append(parts, "'"+strings.ReplaceAll(arg, "'", `'\''`)+"'")
			continue
		}
		if strings.ContainsAny(arg, " \t\n\"'()*?[]<>|&;$`\\") {
			parts = append(parts, strconv.Quote(arg))
			continue
		}
		parts = append(parts, arg)
	}
	line := strings.Join(parts, " ")
	if inv.Stdin != "" {
		line += fmt.Sprintf("   # prompt (%d bytes) on stdin", len(inv.Stdin))
	}
	return line
}

// BuildInvocation assembles the harness command line.
//
// The prompt goes LAST and positionally, following the harnesses themselves:
// both `claude` and `cursor-agent` take it that way. Last matters — a prompt
// beginning with a dash would otherwise be read as a flag, and putting it after
// every flag is what keeps a question like "--model isn't resolving, why?" a
// question rather than a parse error.
//
// `-p` is always passed. sr-agent exists to be called from a hook, and a hook
// has no terminal: an interactive session started there would hang holding the
// whole guardrail open. There is no flag to turn it off because there is no
// caller who wants it off.
// A `--` separator goes between the flags and the prompt, and it is not
// cosmetic. Several of claude's own flags are VARIADIC — `--add-dir
// <directories...>`, `--allowed-tools <tools...>` — and a variadic flag
// immediately before a positional swallows it. Measured: `claude -p --model
// haiku --add-dir /tmp/x "count the lines"` consumed the prompt as a second
// directory and died with "Input must be provided either through stdin or as a
// prompt argument". The separator ends flag parsing, so the prompt is a
// positional no matter which flag precedes it, and it was confirmed to work
// against claude 2.1.218.
//
// This also subsumes the dash-leading-prompt case: after `--`, a prompt reading
// "--model isn't resolving, why?" is text rather than a flag.
// spec.baseArgs come FIRST among the flags (after `-p --model`, before the
// caller's), so a run always carries its harness's required settings — the Claude
// Code isolation `--settings`, most of all — whatever the caller passed. They sit
// before the caller's args, not after, so a caller-supplied flag of the same name
// is the LATER one; where a harness lets a repeated flag override, the caller's
// intent wins over the default, and where it unions (claude's `--allowed-tools`),
// both apply.
//
// The binary comes from resolveBinary(spec, getenv), not bare spec.binary — see
// that function for why a nested invocation cannot trust a PATH lookup of
// "claude" to find the SAME build that is asking for it.
func BuildInvocation(spec harnessSpec, model string, harnessArgs []string, prompt string, getenv func(string) string) Invocation {
	args := make([]string, 0, len(spec.baseArgs)+len(harnessArgs)+5)
	args = append(args, "-p", "--model", model)
	args = append(args, spec.baseArgs...)
	args = append(args, harnessArgs...)
	if spec.stdinPromptAbove > 0 && len(prompt) > spec.stdinPromptAbove {
		return Invocation{Binary: resolveBinary(spec, getenv), Args: args, Stdin: prompt}
	}
	args = append(args, "--", prompt)
	return Invocation{Binary: resolveBinary(spec, getenv), Args: args}
}

// resolveBinary picks the executable BuildInvocation puts in Invocation.Binary.
//
// spec.binary ("claude") is a bare name, and exec.Command resolves a bare name
// through a PATH lookup done AT RUN TIME — a lookup that has no idea which
// build of "claude" is asking for it. sr-agent's own most important caller is a
// guardrail firing from WITHIN an already-running, interactive Claude Code
// session that then shells out to sr-agent to spawn a nested, one-shot
// `claude -p` judge call. Confirmed in a live session: the PARENT session was
// running via CLAUDE_CODE_EXECPATH pointing at claude-code 2.1.229, while a bare
// "claude" on that same machine's PATH resolved to a DIFFERENT, newer build
// (2.1.241, at ~/.local/bin/claude). The nested process then crashed with a bare
// "claude exited with status 1" and no further detail — version skew between
// the parent's actual binary and whatever PATH happens to resolve to is a
// plausible cause: flag and settings-schema differences between builds are
// exactly the kind of thing that fails opaquely rather than with a clear
// "unknown flag" message.
//
// CLAUDE_CODE_EXECPATH is what Claude Code itself sets to the exact binary it
// was launched from, so when it is present AND names a file that actually
// exists, it is a strictly better answer than a PATH lookup: it is the one
// binary guaranteed to be the SAME build as the session that is asking sr-agent
// to spawn a judge. It is only trusted when getenv also shows CLAUDECODE or
// CLAUDE_CODE_ENTRYPOINT set — the same pair claudeCodeSpec.detect checks — so
// a stale CLAUDE_CODE_EXECPATH left over in an unrelated shell (one that is not
// actually running inside Claude Code right now) cannot redirect this to a
// binary that has nothing to do with the caller.
//
// Falling back to spec.binary — today's bare-name PATH lookup — whenever
// CLAUDE_CODE_EXECPATH is unset, empty, or does not point to a file that exists
// is what keeps this a strict improvement: sr-agent invoked standalone, outside
// any live session, has no parent binary to prefer and behaves exactly as
// before.
//
// A nil getenv or a nil spec.detect also falls back rather than panicking. A
// test-only harnessSpec built as a bare struct literal (this package's own
// verify_test.go does exactly that: `harnessSpec{name: "fake", binary: ...}`)
// has no detect at all, and a helper this deep in the call graph should not be
// the thing that turns "a test spec skipped a field it didn't need" into a
// crash.
func resolveBinary(spec harnessSpec, getenv func(string) string) string {
	if getenv == nil || spec.detect == nil {
		return spec.binary
	}
	if !spec.detect(getenv) {
		return spec.binary
	}
	execPath := getenv("CLAUDE_CODE_EXECPATH")
	if execPath == "" {
		return spec.binary
	}
	info, err := os.Stat(execPath)
	if err != nil || info.IsDir() {
		return spec.binary
	}
	return execPath
}
