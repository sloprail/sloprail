package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/checkrun"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/module/modules"
	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/transcript"
)

func newStagedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "staged --needs citation [--amend] | staged --recorded <path>...",
		Short: "The staged files a commit must cite, by the file-guards' `require: citation`",
		Long: `Print, one path per line, the staged files that a file-guard selects and that carry a
` + "`require: citation`" + ` the commit being made must meet.

The candidate change is the index against HEAD (against git's empty tree when HEAD does not exist
yet). With --amend it is the index against HEAD's parent: the amended commit replaces HEAD, so
everything HEAD changed is the commit's again. File-guards are loaded, matched and their
` + "`when`" + ` evaluated exactly as ` + "`sr-checks run`" + ` does over a range, and a requirement whose
` + "`when`" + ` waives a file leaves it out. Nothing is judged, written, staged or committed.

This is the prevention half of the commit-time check: ` + "`run`" + ` and ` + "`verify`" + ` refuse the commit afterwards,
this says before it which files will need a citation. Exits 1 with the reason on stderr when the
change could not be read: no answer is never "nothing is needed".

With --trailers, read a commit message on stdin and resolve each Sloprail-Cites-User /
Sloprail-Cites-Tool line in it against the session transcript, exactly as ` + "`sr-file --cite`" + ` does (the user
pool for -User, tool_result for -Tool), one JSON object per line: {"trailer", "quote", "ok", "error"}.

With --recorded and paths instead, print the citations this session already recorded for them
(` + "`sr-file --cite`" + `), one JSON object per line: {"path", "trailer", "quote"}.`,
		Args: cobra.ArbitraryArgs,
		RunE: runStaged,
	}
	cmd.Flags().String("needs", "", "What to list the staged files for; only 'citation' is known (required)")
	cmd.Flags().Bool("amend", false, "The commit replaces HEAD: judge the index against HEAD's parent")
	cmd.Flags().Bool("trailers", false, "Resolve the Sloprail-Cites-* lines of a commit message read from stdin, as JSON lines")
	cmd.Flags().Bool("recorded", false, "Print the quotes already recorded for the given paths, as JSON lines")
	return cmd
}

func runStaged(cmd *cobra.Command, args []string) error {
	recorded, _ := cmd.Flags().GetBool("recorded")
	if trailers, _ := cmd.Flags().GetBool("trailers"); trailers {
		return runTrailers(cmd)
	}
	if !recorded && len(args) > 0 {
		return fmt.Errorf("sloprail: unexpected arguments %q (paths go with --recorded)", args)
	}
	if recorded {
		return runRecorded(cmd, args)
	}
	if needs, _ := cmd.Flags().GetString("needs"); needs != "citation" {
		return fmt.Errorf("sloprail: --needs %q is not known; the only value is 'citation'", needs)
	}
	amend, _ := cmd.Flags().GetBool("amend")
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("sloprail: working directory: %w", err)
	}
	root, err := gitrepo.Root(cwd)
	if err != nil || root == "" {
		return fmt.Errorf("sloprail: %s is not inside a git repository", cwd)
	}
	root = filepath.Clean(root)
	reg, err := modules.Registry()
	if err != nil {
		return err
	}
	// The config at HEAD is what the user committed before this work: it alone may switch off a
	// protected rule (as in resolveTarget). No HEAD, no such config to trust.
	var trusted []string
	if gitrepo.HasCommits(root) {
		trusted = append(trusted, "HEAD")
	}
	loaded := checkrun.LoadDeclarations(cmd.ErrOrStderr(), root, reg, trusted...)
	paths, err := checkrun.StagedNeedingCitation(checkrun.StagedParams{
		Err: cmd.ErrOrStderr(), Guards: loaded.FileGuards, Root: root, Amend: amend,
	})
	if err != nil {
		return fmt.Errorf("sloprail: %w", err)
	}
	for _, p := range paths {
		fmt.Fprintln(cmd.OutOrStdout(), p)
	}
	return nil
}

// runRecorded prints the quotes the session recorded for paths, in both pools, one JSON line each.
// Nothing recorded (or no session) prints nothing: it is a hint, never a verdict.
func runRecorded(cmd *cobra.Command, paths []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("sloprail: working directory: %w", err)
	}
	root, err := gitrepo.Root(cwd)
	if err != nil || root == "" {
		return fmt.Errorf("sloprail: %s is not inside a git repository", cwd)
	}
	sess := openSession(filepath.Clean(root))
	defer sess.close()
	rec := checkrun.RecordedQuotes(recordedCitations(sess, filepath.Clean(root)), paths,
		[]transcript.SourceType{transcript.SourceUser, transcript.SourceToolResult})
	enc := json.NewEncoder(cmd.OutOrStdout())
	for _, q := range rec {
		if err := enc.Encode(map[string]string{"path": q.Path, "trailer": q.Trailer, "quote": q.Quote}); err != nil {
			return err
		}
	}
	return nil
}

// runTrailers resolves the citation lines of the message on stdin against the session's
// transcript with the resolver `run` uses for a commit's trailers. No session record resolves
// nothing: every line comes back not ok.
func runTrailers(cmd *cobra.Command) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return fmt.Errorf("sloprail: reading the message: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("sloprail: working directory: %w", err)
	}
	root, err := gitrepo.Root(cwd)
	if err != nil || root == "" {
		return fmt.Errorf("sloprail: %s is not inside a git repository", cwd)
	}
	trailers := map[string][]string{}
	for _, l := range changeset.CiteLines(string(raw)) {
		trailers[l.Key] = append(trailers[l.Key], l.Quote)
	}
	sess := openSession(filepath.Clean(root))
	defer sess.close()
	enc := json.NewEncoder(cmd.OutOrStdout())
	emit := func(key, quote string, err error) error {
		row := map[string]any{"trailer": key, "quote": quote, "ok": err == nil}
		if err != nil {
			row["error"] = err.Error()
		}
		return enc.Encode(row)
	}
	if sess.record == "" {
		for key, quotes := range trailers {
			for _, q := range quotes {
				if err := emit(key, q, errors.New("there is no session transcript to resolve it in")); err != nil {
					return err
				}
			}
		}
		return nil
	}
	project := sessionpath.ProjectDirOf(sess.record, cwd)
	resolve := func(req transcript.CitationRequest) (transcript.Citation, error) {
		return transcript.ResolveCitationAcrossSessions(sess.record, project, req)
	}
	cites, missed := changeset.ResolveCitations([]changeset.Commit{{SHA: "(message)", Trailers: trailers}}, resolve)
	for _, c := range cites {
		key := changeset.TrailerCitesUser
		if !slices.Contains(c.SourceTypes, transcript.SourceUser) {
			key = changeset.TrailerCitesTool
		}
		if err := emit(key, c.Quote, nil); err != nil {
			return err
		}
	}
	for _, u := range missed {
		if err := emit(u.Trailer, u.Quote, u.Err); err != nil {
			return err
		}
	}
	return nil
}
