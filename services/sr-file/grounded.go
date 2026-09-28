package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/grounding"
	"github.com/sloprail/sloprail/internal/transcript"
)

// The grounded file verbs: `sr-file write|edit|delete`.
//
// They do what the harness's Write and Edit tools do — same argument names —
// plus one thing those tools cannot: carry the CITATIONS that ground the
// change, as `--cite:<source-types> <quote>` flags. The citation travels on the
// action, never into the file, so the file stays derived text only while a
// guardrail can still check the change was asked for.
//
// Every citation is resolved against the session's trajectory BEFORE a byte is
// touched; one that does not resolve fails the command and nothing is written.
//
// With SR_FILE_RESOLVE_DIR set (resolve mode) the command writes nothing and
// records the change it would make into that directory — see
// grounding.Resolved. That is how sloprail's hook learns, from this binary
// itself, what a pending sr-file call is about to do.

const groundedHelp = `Citations: --cite:<source-types> <quote>, repeatable. <source-types> is
user (the user's own words), tool_result (a tool's output), or both
comma-separated. Each quote must resolve to exactly one entry of the session's
trajectory, or nothing is written. The words must match exactly; whitespace
need not (a line break in the message matches a space in the quote). Quote with
single quotes so the shell leaves it verbatim.

tool_result also resolves in the records of the sub-agents the session
dispatched, so a sub-agent can cite its own tools' output. user resolves only
in the user's messages in the main conversation: a sub-agent's prompt is the
parent agent's, so a sub-agent quotes the user's words exactly as the user
wrote them, and a parent dispatching work that must cite the user pastes the
user's exact words into the prompt.

Flags take their value as the next word or after '='. '--' ends the flags.`

func newGroundedCmd(verb, use, short, long string) *cobra.Command {
	return &cobra.Command{
		Use:                use,
		Short:              short,
		Long:               long + "\n\n" + groundedHelp,
		DisableFlagParsing: true,
		SilenceUsage:       true,
		SilenceErrors:      true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fc, err := grounding.ParseFile(append([]string{verb}, args...))
			if errors.Is(err, grounding.ErrHelp) {
				return cmd.Help()
			}
			if err != nil {
				// Everything ParseFile refuses is the command line itself: a
				// missing path, a flag with no value, an unknown pool.
				return usageError{err}
			}
			return runGrounded(cmd, fc)
		},
	}
}

func newWriteCmd() *cobra.Command {
	return newGroundedCmd(grounding.VerbWrite,
		"write <file-path> [--content <content>] [--cite:<source-types> <quote>]...",
		"Write a file's whole content, grounded in cited words",
		"Write a file, replacing whatever it held — the Write tool's semantics.\n\n"+
			"The content is --content, or stdin when --content is absent (a heredoc:\n"+
			"  sr-file write notes.md --cite:user 'keep a changelog' <<'EOF' ... EOF).")
}

func newEditCmd() *cobra.Command {
	return newGroundedCmd(grounding.VerbEdit,
		"edit <file-path> --old-string <old> --new-string <new> [--replace-all] [--cite:<source-types> <quote>]...",
		"Replace an exact string in a file, grounded in cited words",
		"Replace --old-string with --new-string — the Edit tool's semantics. --old-string\n"+
			"must occur exactly once unless --replace-all is given, and the file must exist.")
}

func newDeleteCmd() *cobra.Command {
	return newGroundedCmd(grounding.VerbDelete,
		"delete <file-path> [--cite:<source-types> <quote>]...",
		"Delete a file, grounded in cited words",
		"Delete a file. It must exist and be a regular file.")
}

func runGrounded(cmd *cobra.Command, fc grounding.FileCommand) error {
	abs, err := filepath.Abs(fc.Path)
	if err != nil {
		return fmt.Errorf("sr-file %s: %w", fc.Verb, err)
	}
	if link, ok := throughLink(abs); ok {
		return fmt.Errorf("sr-file %s: %s goes through the symbolic link %s, so the change would land where the link points, not at the path a rule judges; name the real path", fc.Verb, fc.Path, link)
	}
	resolveDir := os.Getenv(grounding.EnvResolveDir)

	citations, err := resolveCites(fc)
	if err != nil {
		return err
	}

	before, existed, err := currentState(abs, resolveDir)
	if err != nil {
		return fmt.Errorf("sr-file %s: %s: %w", fc.Verb, fc.Path, err)
	}

	after := ""
	summary := ""
	switch fc.Verb {
	case grounding.VerbWrite:
		after = fc.Content
		if !fc.HasContent {
			b, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return fmt.Errorf("sr-file write: read stdin: %w", err)
			}
			after = string(b)
		}
		summary = fmt.Sprintf("wrote %s (%d bytes)", fc.Path, len(after))
	case grounding.VerbEdit:
		if !existed {
			return fmt.Errorf("sr-file edit: %s does not exist; use `sr-file write` to create it", fc.Path)
		}
		n := strings.Count(before, fc.OldString)
		switch {
		case n == 0:
			return fmt.Errorf("sr-file edit: --old-string not found in %s", fc.Path)
		case n > 1 && !fc.ReplaceAll:
			return fmt.Errorf("sr-file edit: --old-string occurs %d times in %s; give more context to make it unique, or pass --replace-all", n, fc.Path)
		}
		if fc.ReplaceAll {
			after = strings.ReplaceAll(before, fc.OldString, fc.NewString)
		} else {
			after = strings.Replace(before, fc.OldString, fc.NewString, 1)
		}
		summary = fmt.Sprintf("edited %s (%d replacement%s)", fc.Path, n, plural(n))
		if !fc.ReplaceAll {
			summary = fmt.Sprintf("edited %s", fc.Path)
		}
	case grounding.VerbDelete:
		if !existed {
			return fmt.Errorf("sr-file delete: %s does not exist", fc.Path)
		}
		summary = fmt.Sprintf("deleted %s", fc.Path)
	}

	if resolveDir != "" {
		return recordResolved(resolveDir, grounding.Resolved{
			Verb: fc.Verb, Path: abs, Existed: existed,
			OldContent: before, NewContent: after, Citations: citations,
		})
	}

	switch fc.Verb {
	case grounding.VerbDelete:
		err = os.Remove(abs)
	default:
		err = writePreservingMode(abs, after)
	}
	if err != nil {
		return fmt.Errorf("sr-file %s: %w", fc.Verb, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "sr-file: %s", summary)
	if len(citations) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), ", grounded in %d citation%s", len(citations), plural(len(citations)))
	}
	fmt.Fprintln(cmd.OutOrStdout())
	for _, c := range citations {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s:%d\n", c.Path, c.Line)
	}
	return nil
}

// resolveCites grounds every --cite: flag, or refuses the whole command. The
// trajectory named is the session's; ResolveCitation searches the user pool in
// its root record and the tool_result pool in the root's and every sub-agent's,
// so a sub-agent's call — whose session id names the root — can ground a change
// in its own tools' output.
func resolveCites(fc grounding.FileCommand) ([]transcript.Citation, error) {
	if len(fc.Cites) == 0 {
		return []transcript.Citation{}, nil
	}
	path := os.Getenv(grounding.EnvTranscript)
	if path == "" {
		path = transcript.CurrentSessionPath("")
	}
	if path == "" {
		return nil, fmt.Errorf("sr-file %s: cannot resolve citations: no session trajectory found (set %s, or run inside a session with %s set)",
			fc.Verb, grounding.EnvTranscript, transcript.SessionIDEnv)
	}
	cs, err := transcript.ResolveCitations(path, fc.Cites)
	if err != nil {
		return nil, fmt.Errorf("sr-file %s: nothing written: %w", fc.Verb, err)
	}
	return cs, nil
}

// throughLink returns the first symbolic link on the way to abs — the target
// itself included — that is not also on the way to the working directory. A
// rule judges the path as spelled; through `notes.md -> .github/ci.yml` the
// bytes land elsewhere. A link above the working directory (macOS's /var) is
// shared with every path a rule sees, and a top-level one (/tmp) no agent made.
func throughLink(abs string) (string, bool) {
	wd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	sep := string(filepath.Separator)
	for p := abs; ; p = filepath.Dir(p) {
		parent := filepath.Dir(p)
		if parent == p || parent == sep || p == wd || strings.HasPrefix(wd, p+sep) {
			return "", false
		}
		if fi, err := os.Lstat(p); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
			return p, true
		}
	}
}

// currentState is the target's content and existence — from the overlay when
// an earlier invocation in the same resolved line already changed it.
func currentState(abs, resolveDir string) (string, bool, error) {
	if resolveDir != "" {
		if _, err := os.Stat(grounding.OverlayDeleted(resolveDir, abs)); err == nil {
			return "", false, nil
		}
		if b, ok := filemod.ReadRegular(grounding.OverlayEntry(resolveDir, abs), filemod.MaxContentReadBytes); ok {
			return b, true, nil
		}
	}
	fi, err := os.Stat(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !fi.Mode().IsRegular() {
		return "", false, fmt.Errorf("not a regular file")
	}
	// The stat above can be raced (a FIFO swapped in) and says nothing of size:
	// read the one safe way — non-blocking open, fstat on the open file, capped.
	b, ok := filemod.ReadRegular(abs, filemod.MaxContentReadBytes)
	if !ok {
		return "", false, fmt.Errorf("not a regular file, or larger than %d bytes", filemod.MaxContentReadBytes)
	}
	return b, true, nil
}

// recordResolved appends r to the resolve directory's record file and updates
// the overlay, so a later invocation in the same line sees this one's result.
func recordResolved(dir string, r grounding.Resolved) error {
	entry := grounding.OverlayEntry(dir, r.Path)
	if err := os.MkdirAll(filepath.Dir(entry), 0o700); err != nil {
		return fmt.Errorf("sr-file %s: resolve: %w", r.Verb, err)
	}
	var err error
	if r.Verb == grounding.VerbDelete {
		_ = os.Remove(entry)
		err = os.WriteFile(grounding.OverlayDeleted(dir, r.Path), nil, 0o600)
	} else {
		_ = os.Remove(grounding.OverlayDeleted(dir, r.Path))
		err = os.WriteFile(entry, []byte(r.NewContent), 0o600)
	}
	if err != nil {
		return fmt.Errorf("sr-file %s: resolve: %w", r.Verb, err)
	}
	line, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("sr-file %s: resolve: %w", r.Verb, err)
	}
	f, err := os.OpenFile(grounding.ResolvedFile(dir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("sr-file %s: resolve: %w", r.Verb, err)
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

func writePreservingMode(abs, content string) error {
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(abs); err == nil {
		mode = fi.Mode().Perm()
	} else if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, []byte(content), mode)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
