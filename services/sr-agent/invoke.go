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

// ParseAllowedTools splits the --allowed-tools value into individual tool names.
//
// The flag takes the SAME comma-or-space-separated form claude's own
// `--allowed-tools <tools...>` documents, so an author who knows one knows this.
// Both separators are honoured and empty fields dropped, so "Read, WebFetch" and
// "Read WebFetch" and "Read,WebFetch" all yield the same two tools and a stray
// comma grants nothing rather than an empty tool name. Returns nil for an empty or
// whitespace-only value, which the caller reads as "grant only what the run itself
// needs".
//
// A tool name is not otherwise validated here: like a concrete model name in a
// model set, whether the harness HAS a tool by that name is the harness's to
// answer, not this binary's — sr-agent's job is to pass the request through in the
// harness's own spelling.
func ParseAllowedTools(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// ErrBadReadDir is returned when a `--read-dir` does not name an existing
// directory.
var ErrBadReadDir = errors.New("invalid --read-dir")

// ErrNoReadConfinement is returned when `--read-dir` is given to a harness with
// no permission model sr-agent can express "read but never write" in.
var ErrNoReadConfinement = errors.New("harness cannot confine a read-only directory")

// resolveReadDirs makes each `--read-dir` absolute and checks it is a directory.
//
// Absolute because a harness permission rule is matched on an absolute path — a
// relative one would be read against whatever the harness takes as its base,
// which is not this process's cwd. Checked because a missing directory would not
// fail loudly anywhere: the agent would simply be denied every read in it, and a
// judge would reach its verdict blind. Duplicates are dropped so one directory
// never becomes two grants.
func resolveReadDirs(raw []string) ([]string, error) {
	var dirs []string
	seen := map[string]bool{}
	for _, entry := range raw {
		if strings.TrimSpace(entry) == "" {
			return nil, fmt.Errorf("%w: an empty value names no directory", ErrBadReadDir)
		}
		abs, err := filepath.Abs(entry)
		if err != nil {
			return nil, fmt.Errorf("%w: %q: %s", ErrBadReadDir, entry, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("%w: %q: %s", ErrBadReadDir, entry, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%w: %q is not a directory", ErrBadReadDir, entry)
		}
		if !seen[abs] {
			seen[abs] = true
			dirs = append(dirs, abs)
		}
	}
	return dirs, nil
}

// harnessGrant turns the access a run needs into the harness's own flags, via
// the harness's grant.
//
// A harness with no grant still gets the caller's tools, as its own
// `--allowed-tools` — but a read-only directory is REFUSED there rather than
// dropped or passed as a plain directory grant. "Read but never write" is a
// promise about what the agent cannot do; a harness that cannot express it would
// either leave the judge blind or hand it write access, and the caller asked for
// neither.
func harnessGrant(spec harnessSpec, g accessGrant) ([]string, error) {
	if spec.grant != nil {
		return spec.grant(g), nil
	}
	if len(g.ReadDirs) > 0 {
		return nil, fmt.Errorf("%w: %s has no permission model sr-agent can express read-only access in; drop --read-dir or run a harness that has one",
			ErrNoReadConfinement, spec.name)
	}
	if len(g.Tools) > 0 {
		return []string{"--allowed-tools", strings.Join(g.Tools, " ")}, nil
	}
	return nil, nil
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

	// Args is its full argument list, prompt included.
	Args []string
}

// String renders the invocation for diagnostics. Arguments containing spaces
// or shell metacharacters are quoted so a printed command can be pasted back
// into a shell and mean the same thing — a prompt is almost always such an
// argument, and a permission rule like `Edit(//dir/**)` is another: unquoted,
// its parentheses are a shell syntax error and its `**` a glob.
func (inv Invocation) String() string {
	parts := make([]string, 0, len(inv.Args)+1)
	parts = append(parts, inv.Binary)
	for _, arg := range inv.Args {
		if strings.ContainsAny(arg, " \t\n\"'()*?[]<>|&;$`\\") {
			parts = append(parts, strconv.Quote(arg))
			continue
		}
		parts = append(parts, arg)
	}
	return strings.Join(parts, " ")
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
