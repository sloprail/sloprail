package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/tuidrive"
)

// tuiSocketEnv names the socket of the TUI session a run is operating: set in the
// environment of the simulated user, which runs these commands.
const tuiSocketEnv = "SR_EVAL_TUI_SOCKET"

func newTUICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tui <type|key|wait>",
		Short: "Operate the agent-under-test's terminal UI (for the simulated user of a run)",
		Long: `Operate the interactive terminal UI of the agent-under-test, as a person at a terminal
would. These are the simulated user's only way to reach it, and they work only inside a run
that is driving one (the run names its session in $` + tuiSocketEnv + `).

Each command prints the screen as it is after the action, then, for a wait on a pattern, a
last line saying whether the pattern appeared.`,
		SilenceUsage: true,
	}
	cmd.AddCommand(tuiTypeCmd(), tuiKeyCmd(), tuiWaitCmd())
	return cmd
}

func tuiTypeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "type <text>",
		Short: "Type text at the input (no Enter: press it with `key enter`)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return tuiCall(cmd, tuidrive.Call{Tool: tuidrive.ToolType, Text: strings.Join(args, " ")})
		},
	}
}

func tuiKeyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "key <name>",
		Short: "Press a key: " + tuidrive.KeyNames(),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return tuiCall(cmd, tuidrive.Call{Tool: tuidrive.ToolKey, Key: args[0]})
		},
	}
}

func tuiWaitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Wait for a pattern to appear on the screen, or (with none) for the screen to settle",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pattern, _ := cmd.Flags().GetString("pattern")
			timeout, _ := cmd.Flags().GetDuration("timeout")
			return tuiCall(cmd, tuidrive.Call{Tool: tuidrive.ToolWait, Pattern: pattern, TimeoutMs: int(timeout / time.Millisecond)})
		},
	}
	cmd.Flags().String("pattern", "", "A regexp matched against the rendered screen; return as soon as it appears")
	cmd.Flags().Duration("timeout", 30*time.Second, "How long to wait at most")
	return cmd
}

func tuiCall(cmd *cobra.Command, c tuidrive.Call) error {
	sock := os.Getenv(tuiSocketEnv)
	if sock == "" {
		return fmt.Errorf("no terminal UI to operate: $%s is not set (these commands work only inside an sr-eval run that drives one)", tuiSocketEnv)
	}
	r, err := tuidrive.Do(sock, c)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, r.Frame)
	if r.Matched != nil {
		if *r.Matched {
			fmt.Fprintln(out, "\n[the pattern appeared]")
		} else {
			fmt.Fprintln(out, "\n[timed out: the pattern did not appear]")
		}
	}
	if r.Error != "" {
		return fmt.Errorf("%s", r.Error)
	}
	return nil
}
