// Command sr-mark writes `// sr:<kind> <fqn>` enforcement markers DIRECTLY into impl
// source files. It is its own STANDALONE binary — NOT a subcommand of the `sloprail` binary —
// because impl markers are a general annotation concept, not guardrail-specific, and may be used
// outside the guardrail flow. It is PURE GO: no store, no cgo (it just edits text files), so it
// builds with a plain `go build`. This is also the shape every high-level sloprail command is
// moving to: one binary per command, with a root `sr` that proxies to them.
//
// The marker KIND is a SUBCOMMAND under the `sr:` namespace. Two kinds today:
//
//	sr-mark blueprint      --user.User.email=path/to/file.go:42 ...   // impl enforcement
//	sr-mark blueprint:test --user.User.email=path/to/file_test.go:9 ...  // test coverage
//
// Each `--<fqn>=<path>:<line>` pair writes ONE `// sr:<kind> <fqn>` marker; many pairs in one
// call write many markers. The flag NAME is the fqn. `blueprint:test` marks the TEST case that
// exercises an invariant — same fqn namespace, a parallel marker kind, checked independently
// (the impl-application checks never look at it, and vice versa). Adding another kind later is
// just another subcommand wired with newKindCmd("<kind>", ...) emitting `// sr:<kind> <fqn>`.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func main() {
	root := newRoot()
	// The marker pairs are `--<fqn>=<path>:<line>`, i.e. the FLAG NAME is an arbitrary fqn.
	// pflag cannot register arbitrary names ahead of time, so pre-scan argv and register each
	// `--<fqn>` token on the matching kind subcommand BEFORE parsing. Known flags (--root, -h)
	// are left to cobra. This keeps cobra's parsing, help, and --root inheritance intact.
	registerDynamicMarkerFlags(root, os.Args[1:])
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// newRoot is the `sr-mark` root. It carries no behaviour of its own — every marker kind is a
// subcommand (currently just `blueprint`). `--root` is inherited by the subcommands for
// resolving relative paths.
func newRoot() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "sr-mark <kind> --<fqn>=<path>:<line> ...",
		Short:         "Write // sr:<kind> <fqn> enforcement markers into impl files",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.PersistentFlags().String("root", "", "Impl tree root for relative paths (default: $SLOPRAIL_GIT_ROOT, else cwd)")
	cmd.AddCommand(newKindCmd("blueprint",
		"Write // sr:blueprint <fqn> markers (enforce guardrail invariants in impl)"))
	cmd.AddCommand(newKindCmd("blueprint:test",
		"Write // sr:blueprint:test <fqn> markers (mark the test case that verifies a guardrail invariant)"))
	cmd.AddCommand(newDeleteCmd())
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
			"EXAMPLE:\n" +
			"  sr-mark delete blueprint order.cancel_only_pending order.cancel_only_from_early_status",
		Args:          cobra.MinimumNArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := args[0]
			fqns := args[1:]
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

// newKindCmd builds the subcommand for one marker kind. The KIND ("blueprint") is captured here;
// the subcommand takes repeated `--<fqn>=<path>:<line>` pairs — the flag NAME is the fqn, the
// value is "path:line" — and writes one `// sr:<kind> <fqn>` marker per pair. Same kind, more
// pairs → more markers in one call. New kind → another newKindCmd call.
func newKindCmd(kind, short string) *cobra.Command {
	return &cobra.Command{
		Use:   kind + " --<fqn>=<path>:<line> [--<fqn>=<path>:<line> ...]",
		Short: short,
		Long: "Write `// sr:" + kind + " <fqn>` enforcement markers into impl source files.\n\n" +
			"Each marker pair is a flag whose NAME is the invariant fqn (<domain>.<name>) and whose\n" +
			"value is <path>:<line> — i.e. `--<fqn>=<path>:<line>`. Pass many pairs in one call to\n" +
			"write many markers. Paths are resolved against --root (default $SLOPRAIL_GIT_ROOT, else cwd);\n" +
			"line is 1-based. Writing is idempotent and uses the right comment leader for the file's\n" +
			"language. All pairs are validated before any file is written.\n\n" +
			"EXAMPLES:\n" +
			"  sr-mark " + kind + " --user.email_unique=src/auth/user.ts:42\n" +
			"  sr-mark " + kind + " --user.email_unique=src/user.ts:42 --order.total_nonneg=src/order.ts:13",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runKind(cmd, kind)
		},
	}
}

// markerKinds is the set of registered kind subcommand names; argv is only pre-scanned for
// dynamic flags when the invoked subcommand is one of these.
func markerKinds(root *cobra.Command) map[string]*cobra.Command {
	kinds := map[string]*cobra.Command{}
	for _, c := range root.Commands() {
		kinds[c.Name()] = c
	}
	return kinds
}

// registerDynamicMarkerFlags scans argv for `sr-mark <kind> --<fqn>[=...] ...` and registers
// every `--<fqn>` token (that is not already a known flag) as a String flag on the kind
// subcommand, so pflag accepts the arbitrary fqn flag names. Persistent flags (e.g. --root) and
// -h/--help are left to cobra.
func registerDynamicMarkerFlags(root *cobra.Command, argv []string) {
	if len(argv) == 0 {
		return
	}
	sub, ok := markerKinds(root)[argv[0]]
	if !ok {
		return
	}
	fs := sub.Flags()
	persist := root.PersistentFlags()
	for _, a := range argv[1:] {
		if !strings.HasPrefix(a, "--") || a == "--" {
			continue
		}
		name := strings.TrimPrefix(a, "--")
		if i := strings.IndexByte(name, '='); i >= 0 {
			name = name[:i]
		}
		if name == "" {
			continue
		}
		// Don't shadow a real flag (e.g. --root, --help).
		if fs.Lookup(name) != nil || persist.Lookup(name) != nil {
			continue
		}
		fs.String(name, "", "marker pair <path>:<line> for fqn "+name)
	}
}

// runKind reads the set marker flags off cmd (the dynamically-registered `--<fqn>=<path>:<line>`
// pairs), resolves each path against --root, and writes one `sr:<kind> <fqn>` marker per pair.
// It validates every pair BEFORE writing any, writes in a STABLE (fqn-sorted) order for
// deterministic output, and is idempotent per marker.go's WriteMarker.
func runKind(cmd *cobra.Command, kind string) error {
	type marker struct {
		fqn  string
		file string // as given (for the message)
		path string // resolved absolute / relative-to-root
		line int
	}

	// Collect only the flags the user actually set, excluding the reserved root/help flags;
	// every remaining set flag is an `--<fqn>=<path>:<line>` pair.
	pairs := map[string]string{}
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if f.Name == "root" || f.Name == "help" {
			return
		}
		pairs[f.Name] = f.Value.String()
	})
	if len(pairs) == 0 {
		return fmt.Errorf("sr-mark %s: at least one --<fqn>=<path>:<line> is required", kind)
	}

	root := markerRoot(cmd)
	fqns := make([]string, 0, len(pairs))
	for fqn := range pairs {
		fqns = append(fqns, fqn)
	}
	sort.Strings(fqns) // deterministic order

	// Parse + validate ALL pairs first so a bad pair fails before any file is touched.
	markers := make([]marker, 0, len(pairs))
	for _, fqn := range fqns {
		file, line, err := parsePathLine(pairs[fqn])
		if err != nil {
			return fmt.Errorf("sr-mark %s: --%s=%q: %w", kind, fqn, pairs[fqn], err)
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
		if err := WriteMarker(m.path, kind, m.fqn, m.line); err != nil {
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
