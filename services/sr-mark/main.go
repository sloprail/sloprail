// Command sr-mark writes `// sr:<kind> <fqn>` enforcement markers DIRECTLY into impl
// source files. It is its own STANDALONE binary — NOT a subcommand of the `sloprail` binary —
// because impl markers are a general annotation concept, not guardrail-specific, and may be used
// outside the guardrail flow. It is PURE GO: no store, no cgo (it just edits text files), so it
// builds with a plain `go build`. This is also the shape every high-level sloprail command is
// moving to: one binary per command, with a root `sr` that proxies to them.
//
// The verbs are FIXED and the marker KIND is an ARGUMENT to them:
//
//	sr-mark apply <kind> --<fqn>=<path>:<line> [--<fqn>=<path>:<line> ...]
//	sr-mark delete <kind> <fqn> [<fqn> ...]
//
// Kind is DATA, not a subcommand. A kind is whatever a project needs to mark — the engine ships
// no list of them (see the Marker model in the sloprail-service spec: "Not drawn from a fixed
// set"). So `sr-mark apply docs --x.y=f.go:1` works with no code change, exactly like
// `blueprint` or any other word. Registering a subcommand per kind would have meant editing Go
// to add one, which a freeform vocabulary cannot tolerate.
//
// Each `--<fqn>=<path>:<line>` pair writes ONE `// sr:<kind> <fqn>` marker; many pairs in one
// call write many markers. The flag NAME is the fqn.
//
// Because the fqn is the flag NAME, pflag cannot register the flags ahead of time. With a fixed
// verb the argument shape is unambiguous — `apply`, then the kind, then only `--<fqn>=<value>`
// pairs — so apply parses its own pairs off the raw argument tail rather than pre-scanning argv
// to teach cobra about names it invented. Only `--root` and `-h`/`--help` are real flags.
//
// Both fields are validated at this boundary, before any file is touched, per the spec's Marker
// model: kind is alphanumeric (plus `:`, so a kind may namespace itself as `blueprint:test`),
// and fqn is restricted to what a URL admits AND must carry no whitespace. The whitespace rule
// is load-bearing rather than cosmetic: the written form is `sr:<kind> <fqn>` on one line, and
// the reader in marker.go is anchored — `sr:<kind>\s+(\S+)\s*$` — so a space-bearing fqn writes
// to the file cleanly and then matches NOTHING when read back. The marker is not merely
// truncated; it is invisible to every reader while sitting in the file. That is silent
// corruption, so it is refused here at write time.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/version"
)

func main() {
	if err := newRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// newRoot is the `sr-mark` root. It carries no behaviour of its own — it hosts the two fixed
// verbs, `apply` and `delete`, each of which takes the marker kind as its first argument.
// `--root` is inherited by both for resolving relative paths.
func newRoot() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "sr-mark <command> <kind> ...",
		Short:         "Write // sr:<kind> <fqn> enforcement markers into impl files",
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.PersistentFlags().String("root", "", "Impl tree root for relative paths (default: $SLOPRAIL_GIT_ROOT, else cwd)")
	cmd.AddCommand(newApplyCmd())
	cmd.AddCommand(newDeleteCmd())
	return cmd
}

// newApplyCmd builds `sr-mark apply <kind> --<fqn>=<path>:<line> [...]`. The kind is the first
// positional argument, so any kind works without a code change.
//
// FlagParsing is disabled for this command: the `--<fqn>=<value>` pairs have arbitrary names that
// pflag cannot know in advance, so apply reads the argument tail itself (parseApplyArgs) instead
// of asking cobra to parse names that do not exist as flags. `--root` and `-h`/`--help` are
// recognised by that parser and handled explicitly.
func newApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply <kind> --<fqn>=<path>:<line> [--<fqn>=<path>:<line> ...]",
		Short: "Write // sr:<kind> <fqn> markers into impl files",
		Long: "Write `// sr:<kind> <fqn>` enforcement markers into impl source files.\n\n" +
			"KIND is the first argument and is free-form — the marker vocabulary is the project's,\n" +
			"not this tool's, so any kind works with no code change. It must be alphanumeric, and may\n" +
			"use `:` to namespace itself (e.g. `blueprint:test`), `_`, `-` and `.`.\n\n" +
			"Each marker pair is a flag whose NAME is the invariant fqn and whose value is\n" +
			"<path>:<line> — i.e. `--<fqn>=<path>:<line>`. Pass many pairs in one call to write many\n" +
			"markers. An fqn may use any character a URL admits, but must contain NO whitespace: a\n" +
			"marker is one line of text, so a name with a space in it would write cleanly and then be\n" +
			"unreadable — the marker reader is line-anchored and would not match it at all.\n\n" +
			"Paths are resolved against --root (default $SLOPRAIL_GIT_ROOT, else cwd); line is 1-based.\n" +
			"Writing is idempotent and uses the right comment leader for the file's language. All\n" +
			"pairs are validated before any file is written.\n\n" +
			"EXAMPLES:\n" +
			"  sr-mark apply blueprint --user.email_unique=src/auth/user.ts:42\n" +
			"  sr-mark apply blueprint:test --user.email_unique=src/auth/user_test.ts:9\n" +
			"  sr-mark apply docs --order.Cart.total=src/order.ts:13 --user.email_unique=src/user.ts:42",
		DisableFlagParsing: true,
		SilenceUsage:       true,
		SilenceErrors:      true,
		RunE: func(cmd *cobra.Command, args []string) error {
			parsed, err := parseApplyArgs(args)
			if err != nil {
				return err
			}
			if parsed.help {
				return cmd.Help()
			}
			return runApply(cmd, parsed)
		},
	}
	return cmd
}

// newDeleteCmd builds `sr-mark delete <kind> <fqn> [<fqn> ...]`: find and remove every
// `// sr:<kind> <fqn>` marker comment for the given fqns, wherever it lives in the impl tree.
// Deleting a rule row typically leaves a now-meaningless marker behind; this removes it WITHOUT
// the agent having to grep the tree + hand-edit each hit.
func newDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <kind> <fqn> [<fqn> ...]",
		Short: "Remove // sr:<kind> <fqn> marker comments for the given fqns, wherever found",
		Long: "Scan the impl tree (--root, default $SLOPRAIL_GIT_ROOT or cwd) for `// sr:<kind> <fqn>`\n" +
			"(or `# …` / `-- …` for SQL) comments naming any of the given fqns, and remove each matching\n" +
			"comment LINE. A marker that also carries other text on the same line is left untouched\n" +
			"(the scanner only matches lines that ARE a marker comment, mirroring how sr-mark writes\n" +
			"them on their own line). Silently no-ops for an fqn with no marker anywhere.\n\n" +
			"KIND is the first argument and is free-form, same as for `apply`; the fqns follow.\n\n" +
			"EXAMPLE:\n" +
			"  sr-mark delete blueprint order.cancel_only_pending order.cancel_only_from_early_status",
		Args:          cobra.MinimumNArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := args[0]
			fqns := args[1:]
			if err := validateKind(kind); err != nil {
				return err
			}
			for _, fqn := range fqns {
				if err := validateFQN(fqn); err != nil {
					return err
				}
			}
			root := markerRoot(cmd)
			n, err := DeleteMarkers(root, kind, fqns)
			if err != nil {
				return err
			}
			cmd.Printf("removed %d marker(s)\n", n)
			return nil
		},
	}
	return cmd
}

// applyArgs is the parsed form of `apply`'s argument tail: the marker kind, the
// `--<fqn>=<path>:<line>` pairs in the order given, and the two real flags apply understands.
type applyArgs struct {
	kind  string
	pairs map[string]string
	root  string
	help  bool
}

// parseApplyArgs reads `apply`'s raw argument tail: `<kind>` followed by `--<fqn>=<path>:<line>`
// pairs, with `--root <dir>` / `--root=<dir>` and `-h`/`--help` recognised as the only real flags.
//
// This exists because the fqn is the flag NAME, so the names cannot be registered with pflag in
// advance. The old code solved that by pre-scanning os.Args and declaring each `--<fqn>` token as
// a string flag on the matching kind subcommand — which, because it mapped EVERY child of root as
// a "kind", also let `sr-mark delete blueprint --foo` silently register `--foo` and succeed. With
// a fixed verb the shape is unambiguous, so the tail is parsed directly here and anything that is
// not a recognised flag or a well-formed pair is an ERROR rather than a silently-accepted flag.
func parseApplyArgs(args []string) (applyArgs, error) {
	out := applyArgs{pairs: map[string]string{}}
	// A bare `sr-mark apply` (or with only -h) should print help rather than error.
	for _, a := range args {
		if a == "-h" || a == "--help" {
			out.help = true
			return out, nil
		}
	}
	if len(args) == 0 {
		out.help = true
		return out, nil
	}

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--root":
			if i+1 >= len(args) {
				return out, fmt.Errorf("sr-mark apply: --root requires a value")
			}
			i++
			out.root = args[i]
		case strings.HasPrefix(a, "--root="):
			out.root = strings.TrimPrefix(a, "--root=")
		case strings.HasPrefix(a, "--"):
			name := strings.TrimPrefix(a, "--")
			// Split on the LAST `=`, not the first. An fqn may legitimately contain `=` — a URL
			// query string like `https://ex.com/a?b=c` is exactly the kind of name the spec's
			// "whatever identifies the thing in the project's own terms: a dotted name, a path, a
			// URL" invites — whereas the VALUE is always `<path>:<line>` and never contains one.
			// So the last `=` is unambiguously the separator, mirroring parsePathLine splitting on
			// the last `:` for the same reason.
			eq := strings.LastIndexByte(name, '=')
			if eq < 0 {
				return out, fmt.Errorf("sr-mark apply: %q must be given as --<fqn>=<path>:<line>", a)
			}
			fqn, value := name[:eq], name[eq+1:]
			if err := validateFQN(fqn); err != nil {
				return out, err
			}
			if _, dup := out.pairs[fqn]; dup {
				return out, fmt.Errorf("sr-mark apply: fqn %q given more than once", fqn)
			}
			out.pairs[fqn] = value
		case strings.HasPrefix(a, "-") && a != "-":
			return out, fmt.Errorf("sr-mark apply: unknown flag %q", a)
		default:
			if out.kind != "" {
				return out, fmt.Errorf("sr-mark apply: unexpected argument %q — the only positional argument is <kind>", a)
			}
			out.kind = a
		}
	}

	if out.kind == "" {
		return out, fmt.Errorf("sr-mark apply: <kind> is required: sr-mark apply <kind> --<fqn>=<path>:<line>")
	}
	if err := validateKind(out.kind); err != nil {
		return out, err
	}
	if len(out.pairs) == 0 {
		return out, fmt.Errorf("sr-mark apply %s: at least one --<fqn>=<path>:<line> is required", out.kind)
	}
	return out, nil
}

// kindAllowed reports whether r may appear in a marker kind: alphanumeric, plus the separators a
// kind uses to namespace itself. `:` is kept legal deliberately — a kind like `blueprint:test` is
// a parallel kind checked independently of `blueprint`, and since the written form is
// `sr:<kind> <fqn>` with the fqn separated by a SPACE, a `:` inside the kind is unambiguous to
// read back.
func kindAllowed(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == ':' || r == '_' || r == '-' || r == '.':
		return true
	}
	return false
}

// validateKind enforces the kind rule: non-empty and alphanumeric (with `:`/`_`/`-`/`.`). The
// vocabulary itself is the project's — no list of known kinds is checked, per the spec's Marker
// model — but the CHARACTERS are constrained so the written `sr:<kind> <fqn>` form stays readable
// by the regex in marker.go.
func validateKind(kind string) error {
	if kind == "" {
		return fmt.Errorf("sr-mark: kind must not be empty")
	}
	for _, r := range kind {
		if !kindAllowed(r) {
			return fmt.Errorf("sr-mark: invalid kind %q: %q is not allowed — a kind must be alphanumeric (`:`, `_`, `-` and `.` are also allowed)", kind, r)
		}
	}
	return nil
}

// fqnAllowed reports whether r may appear in an fqn: the unreserved and reserved character sets a
// URL admits (RFC 3986), minus whitespace. Everything a URL allows is permitted because an fqn
// names a thing in the project's own terms — a dotted name, a path, a URL — and the tool has no
// business narrowing that vocabulary.
func fqnAllowed(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	// RFC 3986 unreserved marks, sub-delims, gen-delims, plus % for percent-encoding.
	return strings.ContainsRune("-._~:/?#[]@!$&'()*+,;=%", r)
}

// validateFQN enforces the fqn rule: non-empty, every character one a URL admits, and NO
// whitespace.
//
// The whitespace carve-out is the load-bearing half. The written form is `sr:<kind> <fqn>` on a
// single line, and the reader in marker.go is `^\s*(?://|#|--)\s*sr:<kind>\s+(\S+)\s*$` — anchored
// at BOTH ends. An fqn containing a space therefore writes to the file intact and then matches
// nothing at all on read: `// sr:blueprint a b` is not read as the truncated "a", it is not read
// as a marker at all. The result is a marker that exists in the source, is invisible to every
// tool that looks for it, and can never be deleted by the fqn that created it. That is corruption
// rather than an inconvenience, so it is refused at write time.
func validateFQN(fqn string) error {
	if fqn == "" {
		return fmt.Errorf("sr-mark: fqn must not be empty")
	}
	for _, r := range fqn {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f' {
			return fmt.Errorf("sr-mark: invalid fqn %q: whitespace is not allowed — a marker is written as `sr:<kind> <fqn>` on one line, and a space-bearing name would write cleanly but never be readable again", fqn)
		}
		if !fqnAllowed(r) {
			return fmt.Errorf("sr-mark: invalid fqn %q: %q is not allowed — an fqn may only use characters a URL admits", fqn, r)
		}
	}
	return nil
}

// runApply resolves each `--<fqn>=<path>:<line>` pair against --root and writes one
// `sr:<kind> <fqn>` marker per pair. It validates every pair BEFORE writing any, writes in a
// STABLE (fqn-sorted) order for deterministic output, and is idempotent per marker.go's
// WriteMarker.
func runApply(cmd *cobra.Command, parsed applyArgs) error {
	type marker struct {
		fqn  string
		file string // as given (for the message)
		path string // resolved absolute / relative-to-root
		line int
	}

	root := markerRoot(cmd)
	if parsed.root != "" {
		root = parsed.root
	}

	fqns := make([]string, 0, len(parsed.pairs))
	for fqn := range parsed.pairs {
		fqns = append(fqns, fqn)
	}
	sort.Strings(fqns) // deterministic order

	// Parse + validate ALL pairs first so a bad pair fails before any file is touched.
	markers := make([]marker, 0, len(parsed.pairs))
	for _, fqn := range fqns {
		file, line, err := parsePathLine(parsed.pairs[fqn])
		if err != nil {
			return fmt.Errorf("sr-mark apply %s: --%s=%q: %w", parsed.kind, fqn, parsed.pairs[fqn], err)
		}
		path := file
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, file)
		}
		markers = append(markers, marker{fqn: fqn, file: file, path: path, line: line})
	}

	// Write order matters: WriteMarker inserts a comment line at (line-1), shifting every line
	// BELOW it down by one. Each WriteMarker re-reads the file fresh, so two markers in the SAME
	// file computed against the ORIGINAL content will mis-anchor if a lower-line marker is written
	// first — it pushes the higher-line marker's target down. Writing bottom-up (DESCENDING line
	// within each file) makes every insertion land below all not-yet-written markers, so no target
	// shifts. Markers in different files are independent. (Sort is stable-safe: distinct files keep
	// their relative order; within a file, highest line first.) The user-facing "marked …" lines
	// are still emitted in the original fqn-sorted order for readable output.
	writeOrder := make([]marker, len(markers))
	copy(writeOrder, markers)
	sort.SliceStable(writeOrder, func(i, j int) bool {
		if writeOrder[i].path != writeOrder[j].path {
			return writeOrder[i].path < writeOrder[j].path
		}
		return writeOrder[i].line > writeOrder[j].line // descending line within a file
	})
	for _, m := range writeOrder {
		if err := WriteMarker(m.path, parsed.kind, m.fqn, m.line); err != nil {
			return err
		}
	}
	for _, m := range markers {
		cmd.Printf("marked %s at %s:%d\n", m.fqn, m.file, m.line)
	}
	return nil
}

// parsePathLine splits a `<path>:<line>` value into its path and 1-based line. The line is the
// suffix after the LAST `:`, so a path may itself contain colons. Returns a clear error for a
// missing/empty path or a non-positive / non-numeric line.
func parsePathLine(v string) (path string, line int, err error) {
	i := strings.LastIndex(v, ":")
	if i < 0 {
		return "", 0, fmt.Errorf("expected <path>:<line>")
	}
	path = v[:i]
	lineStr := v[i+1:]
	if path == "" {
		return "", 0, fmt.Errorf("empty path in <path>:<line>")
	}
	line, err = strconv.Atoi(lineStr)
	if err != nil {
		return "", 0, fmt.Errorf("line %q is not a number", lineStr)
	}
	if line <= 0 {
		return "", 0, fmt.Errorf("line must be >= 1, got %d", line)
	}
	return path, line, nil
}

// markerRoot resolves the impl tree for relative paths: --root > $SLOPRAIL_GIT_ROOT > cwd.
func markerRoot(cmd *cobra.Command) string {
	if r, _ := cmd.Flags().GetString("root"); r != "" {
		return r
	}
	if r := os.Getenv("SLOPRAIL_GIT_ROOT"); r != "" {
		return r
	}
	r, _ := os.Getwd()
	return r
}
