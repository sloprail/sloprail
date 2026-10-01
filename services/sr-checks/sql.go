package main

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

func newSQLCmd() *cobra.Command {
	var family bool
	cmd := &cobra.Command{
		Use:   "sql '<select>'",
		Short: "Run a read-only SELECT over the check tables",
		Long: `Run a read-only SELECT over the session's check results and print the rows as
a JSON array.

Only a SELECT (or a WITH ... SELECT) is accepted, and the connection is read-only
besides, so nothing a statement says can change the record. The tables are
check_runs, checks and check_items — a10n's shape; a rule's identity is
check_runs.check_id, its range base_ref..head_ref, and a check's outcome is
checks.status with its findings in checks.metadata (JSON).

EXAMPLES:
  sr-checks sql 'select check_id, head_ref, error from check_runs order by run_at desc'
  sr-checks sql "select subject, kind, status from checks where status = 'fail'"
  sr-checks sql "select json_extract(metadata, '$.reasoning') as why from checks where status = 'fail'"

--family runs the SELECT over every store of the session family — the root session's
and those of its sub-agents, which keep their own stores under their own worktrees —
and concatenates the rows, each with a "_store" field naming its database. A merge gate
asks this: a refusal a sub-agent's Stop recorded is the coordinator's to respect. A store
of the family that cannot be read is an error, never an empty answer.

Unlike ` + "`status`" + `, nothing recorded yet IS an error here: there are no tables to query.
(With --family, no store at all is an empty list.)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			if family {
				rows, err := queryFamily(args[0])
				if err != nil {
					return err
				}
				return enc.Encode(rows)
			}
			store, err := openChecks()
			if err != nil {
				return err
			}
			defer store.Close()
			rows, err := store.Query(args[0])
			if err != nil {
				return err
			}
			return enc.Encode(rows)
		},
	}
	cmd.Flags().BoolVar(&family, "family", false, "Run over every store of the session family (root and sub-agents)")
	return cmd
}
