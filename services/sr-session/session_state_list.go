package main

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

// newSessionStateListCmd returns the entries under a prefix.
//
// This is what makes per-entry storage workable: a rule holding many subjects
// writes each independently and gets the group back by asking for the prefix
// they share, instead of keeping an index of its own keys. No prefix means
// everything this owner has stored.
//
// One JSON object per line, each with the key and the value. A line carries the
// key because a rule listing a group needs to know which subject each answer
// belongs to — values alone would give it the group's contents with no way to
// tell them apart. One object per line rather than one array so a hook can pipe
// the output through the line-oriented tools it already uses, and so a large
// group does not have to be held whole to be read.
func newSessionStateListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list [prefix]",
		Short: "The entries this owner stored under a prefix",
		Long: `The entries this owner stored under a prefix.

One JSON object per line, each with "key" and "value". No prefix means
everything this owner has stored.

The value is the text that was stored, carried as a JSON string, so reading it
back means asking for it raw:

  sr-session state list pending/ | jq -r .value

Without -r the value arrives escaped — a stored JSON document comes back as a
quoted string rather than as an object. To work with each value as JSON:

  sr-session state list pending/ | jq -r .value | jq .done`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var prefix string
			if len(args) == 1 {
				prefix = args[0]
			}

			ownerFlag, _ := cmd.Flags().GetString("owner")
			store, owner, err := openSessionState(ownerFlag)
			if err != nil {
				return err
			}
			defer store.Close()

			entries, err := store.ListState(owner, prefix)
			if err != nil {
				return err
			}

			enc := json.NewEncoder(cmd.OutOrStdout())
			for _, e := range entries {
				// The value is the rule's own text, emitted as the string it
				// is. Decoding it here would make the engine an interpreter of
				// what it promised to treat as opaque, and would fail outright
				// on a rule that stored something other than JSON.
				if err := enc.Encode(stateLine{Key: e.Key, Value: e.Value}); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// stateLine is one entry as `list` prints it.
type stateLine struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
