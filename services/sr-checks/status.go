package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/checkstore"
)

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Each rule's latest run and its checks",
		Long: `Each file-guard's latest evaluation this session, one line per check.

A rule that was evaluated and had nothing to check (its match selected nothing)
shows as a pass with no check. A rule whose evaluation failed as an engine — a git
error, a range that could not be computed — shows as an error carrying the
message: a range that could not be read is never shown as an empty one.

--failing keeps only what is failing: a fail, an error, or an interrupted check.
--rule limits the listing to one rule, by folder name or qualified name.
--json prints the rows as a JSON array (rule, subject, kind, status, base_ref,
head_ref, run_at, fingerprint, error, metadata).

A run still being recorded reads as interrupted: that can be transient — a Stop in
flight — so look again before treating it as a failure. A run that died half-way
stays interrupted, and is never counted as a pass.

Nothing recorded yet is not an error: the listing is empty.`,
		Args: cobra.NoArgs,
		RunE: runStatus,
	}
	cmd.Flags().Bool("failing", false, "Only what is failing")
	cmd.Flags().String("rule", "", "Only this rule (folder name or qualified name)")
	cmd.Flags().Bool("json", false, "Print the rows as JSON")
	return cmd
}

func runStatus(cmd *cobra.Command, _ []string) error {
	failing, _ := cmd.Flags().GetBool("failing")
	rule, _ := cmd.Flags().GetString("rule")
	asJSON, _ := cmd.Flags().GetBool("json")

	rows := []checkstore.CheckStatusRow{}
	store, err := openChecks()
	switch {
	case errors.Is(err, checkstore.ErrNoStore):
		// Nothing has been checked: an empty answer, not a fault.
	case err != nil:
		return err
	default:
		defer store.Close()
		if rows, err = store.CheckStatus(failing, rule); err != nil {
			return err
		}
	}

	if asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	for _, r := range rows {
		fmt.Fprintln(cmd.OutOrStdout(), formatRow(r))
	}
	return nil
}

// formatRow is one status line: rule, status, the check, the range, and what it found.
func formatRow(r checkstore.CheckStatusRow) string {
	line := fmt.Sprintf("%-6s %s", r.Status, r.Rule)
	if r.Kind != "" {
		line += fmt.Sprintf("  %s/%s", r.Subject, r.Kind)
	}
	line += fmt.Sprintf("  %s..%s", short(r.BaseRef), short(r.HeadRef))
	switch {
	case r.Error != "":
		line += "  " + r.Error
	default:
		if why, ok := r.Metadata["reasoning"].(string); ok && why != "" {
			line += "  " + why
		} else if why, ok := r.Metadata["reason"].(string); ok && why != "" {
			line += "  " + why
		}
	}
	return line
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
