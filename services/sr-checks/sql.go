package main

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

func newSQLCmd() *cobra.Command {
	return &cobra.Command{
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

Unlike ` + "`status`" + `, nothing recorded yet IS an error here: there are no tables to query.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openChecks()
			if err != nil {
				return err
			}
			defer store.Close()
			rows, err := store.Query(args[0])
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(rows)
		},
	}
}
