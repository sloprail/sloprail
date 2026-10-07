package main

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/gitrepo"
)

// exitError carries a process exit code out of a command. `find` uses three: 0 a marker was
// found, 1 none was, 2 the committed tree could not be read (a caller must treat that as a
// refusal, never as "none").
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

func exitCode(err error) int {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return 1
}

// newFindCmd builds `sr-mark find <kind> [--fqn F] [--rev R]`: the read half of apply.
func newFindCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "find <kind> [--fqn <fqn>] [--rev <rev>]",
		Short: "List the // sr:<kind> markers committed at a revision (read-only)",
		Long: "Scan the COMMITTED tree at --rev (default HEAD) of the repository --root (default\n" +
			"$SLOPRAIL_GIT_ROOT, else cwd) for `sr:<kind>` markers, reading each file with the engine's own\n" +
			"marker reader: the one a file-guard's `markers` field and `sr-mark apply` use, so what is\n" +
			"found here is exactly what a rule would see. The working tree is never read: an\n" +
			"uncommitted marker is not found.\n\n" +
			"Prints `<path>:<line> <kind> <fqn>` per marker. With --fqn only markers naming that fqn.\n\n" +
			"EXIT STATUS: 0 a marker was found; 1 none was; 2 the tree could not be read (not a git\n" +
			"repository, unknown revision, git failed), with the reason on stderr. 2 is never \"none\".\n\n" +
			"EXAMPLE:\n" +
			"  sr-mark find ci --fqn verify",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          runFind,
	}
	cmd.Flags().String("fqn", "", "Only markers naming this fqn")
	cmd.Flags().String("rev", "HEAD", "The revision whose committed tree is scanned")
	return cmd
}

// sr:invariant cli/mark-find-reads-committed-markers-only
func runFind(cmd *cobra.Command, args []string) error {
	fail := func(format string, a ...any) error {
		return &exitError{code: 2, msg: "sr-mark find: " + fmt.Sprintf(format, a...)}
	}
	kind := args[0]
	if err := validateKind(kind); err != nil {
		return fail("%v", err)
	}
	fqn, _ := cmd.Flags().GetString("fqn")
	rev, _ := cmd.Flags().GetString("rev")
	if strings.HasPrefix(rev, "-") {
		return fail("revision %q looks like an option", rev)
	}
	dir := markerRoot(cmd)
	var stderr bytes.Buffer
	sha := exec.Command("git", "-C", dir, "rev-parse", "--verify", "-q", rev+"^{commit}")
	sha.Stderr = &stderr
	shaOut, err := sha.Output()
	if err != nil {
		return fail("%s is not a commit of the git repository at %s", rev, dir)
	}
	commit := strings.TrimSpace(string(shaOut))
	// Candidate files: any committed text file mentioning `sr:<kind>`; the engine's reader then
	// decides what is a marker. -I skips binaries; a grep miss (exit 1) is "no candidates".
	grep := exec.Command("git", "-C", dir, "grep", "-l", "-I", "-z", "-F", "-e", "sr:"+kind, commit, "--", ":/")
	stderr.Reset()
	grep.Stderr = &stderr
	out, err := grep.Output()
	if err != nil {
		var ee *exec.ExitError
		if !(errors.As(err, &ee) && ee.ExitCode() == 1 && stderr.Len() == 0) {
			return fail("git grep failed in %s: %v %s", dir, err, strings.TrimSpace(stderr.String()))
		}
	}
	found := 0
	for _, entry := range strings.Split(string(out), "\x00") {
		path := strings.TrimPrefix(entry, commit+":")
		if entry == "" || path == entry {
			continue
		}
		text, err := gitrepo.BlobRaw(dir, commit+":"+path)
		if err != nil {
			return fail("could not read %s at %s: %v", path, rev, err)
		}
		for _, m := range filemod.Scan(text) {
			if m.Kind != kind || (fqn != "" && m.FQN != fqn) {
				continue
			}
			found++
			fmt.Fprintf(cmd.OutOrStdout(), "%s:%d %s %s\n", path, m.Line, m.Kind, m.FQN)
		}
	}
	if found == 0 {
		return &exitError{code: 1, msg: ""}
	}
	return nil
}
