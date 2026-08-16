package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// newSessionStateSetCmd stores a value under a key.
//
// It replaces rather than merges. Every merge policy is a guess about what the
// rule meant — whether a nested object is combined or overwritten, a list
// appended or replaced, an explicit null removing a field or setting one — and
// a rule wanting the other answer could not get it. A rule that wants to merge
// composes, using the tool a hook already reaches for:
//
//	sr-session state get x | jq '.done = true' | sr-session state set x
//
// which is also why the value may come from standard input: that pipeline
// depends on it.
func newSessionStateSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> [value]",
		Short: "Store a value under a key, replacing what was there",
		Long: `Store a value under a key, replacing what was there.

The value is the second argument, or standard input when it is omitted. A
trailing newline from a pipeline is dropped, so a value written through a pipe
equals the same value written directly.

It REPLACES; it does not merge. Every merge policy is a guess about what the
rule meant, and a rule wanting the other answer could not get it. Compose
instead, with the tool a hook already reaches for:

  sr-session state get x | jq '.done = true' | sr-session state set x

Which owner is asking comes from --owner, and is required. Nothing stops a
caller naming another plugin; separate records rest on callers naming themselves
honestly, not on anything enforcing it.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			value, err := setValue(cmd, args)
			if err != nil {
				return err
			}

			ownerFlag, _ := cmd.Flags().GetString("owner")
			store, owner, err := openSessionState(ownerFlag)
			if err != nil {
				return err
			}
			defer store.Close()

			return store.SetState(owner, args[0], value)
		},
	}
}

// setValue is the value to store: the argument when given, standard input
// otherwise.
//
// The trailing newline a pipeline leaves is dropped, since it belongs to the
// pipe rather than to what the rule meant to store — without that, a value
// written by `get | jq | set` would not equal the one written directly.
func setValue(cmd *cobra.Command, args []string) (string, error) {
	if len(args) == 2 {
		return args[1], nil
	}
	body, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", fmt.Errorf("sloprail: read value from stdin: %w", err)
	}
	return strings.TrimSuffix(string(body), "\n"), nil
}
