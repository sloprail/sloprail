// Command sr-checks reads what a session's file-guards concluded about its
// commits. It is its own STANDALONE binary — one binary per command, with the
// root `sr` proxying to it — and it only READS.
//
//	sr-checks status [--failing] [--rule X] [--json]   each rule's latest run and its checks
//	sr-checks sql '<select>'                            any read-only SELECT over the tables
//
// The results are written by the Stop evaluation in sr-session, into a database
// beside the session's state; this opens it read-only, so nothing here can
// create it, migrate it or change what a session concluded. Which database is
// the session's is decided by internal/sessionpath, the same code sr-session
// uses, so both find the same file.
//
// The tables — check_runs, checks, check_items — are a10n's check-results store,
// with sloprail's data inside (see internal/checkstore).
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/transcript"
	"github.com/sloprail/sloprail/internal/version"
)

func main() {
	if err := newRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "sr-checks <command>",
		Short: "What this session's file-guards concluded about its commits",
		Long: `What this session's file-guards concluded about its commits.

A file-guard judges commits: at Stop each rule is evaluated over the range of
commits it has not yet passed, and every evaluation is recorded — the range, and
one row per check with its status (pass, fail, skip, error), the fingerprint it
is cached by, and what it found. These commands read that record. They change
nothing.

  sr-checks status [--failing] [--rule X] [--json]   the latest run of each rule
  sr-checks sql '<select>'                            any read-only SELECT

The session is the current one, found from the environment (CLAUDE_CODE_SESSION_ID)
the way ` + "`sr-session trajectory cite`" + ` finds it. The tables are check_runs, checks
and check_items; ` + "`sr-checks sql 'select name from sqlite_master'`" + ` lists them.`,
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newStatusCmd(), newSQLCmd())
	return root
}

// openChecks opens the current session's check results read-only.
//
// checkstore.ErrNoStore is returned as itself — nothing has been checked in this
// session yet — because the two commands treat it differently.
func openChecks() (checkstore.Store, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("sr-checks: working directory: %w", err)
	}
	record := transcript.CurrentSessionPath(cwd)
	if record == "" {
		return nil, fmt.Errorf("sr-checks: no session to read — run this inside a session (with %s set) whose transcript exists", transcript.SessionIDEnv)
	}
	id, err := sessionpath.StableIdentity(record, cwd)
	if err != nil {
		return nil, fmt.Errorf("sr-checks: session identity: %w", err)
	}
	path, err := sessionpath.ChecksDB(cwd, id.ID)
	if err != nil {
		return nil, err
	}
	store, err := checkstore.OpenReadOnly(path)
	if err != nil {
		if errors.Is(err, checkstore.ErrNoStore) {
			return nil, err
		}
		return nil, fmt.Errorf("sr-checks: %w", err)
	}
	return store, nil
}
