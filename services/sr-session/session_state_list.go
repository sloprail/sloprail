package main

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/sessionstate"
)

// newSessionStateListCmd returns the entries under a prefix.
//
// This is what makes per-entry storage workable: a rule holding many subjects
// writes each independently and gets the group back by asking for the prefix
// they share, instead of keeping an index of its own keys. No prefix means
// everything this guardrail has stored.
//
// One JSON object per line, each with the key and the value. A line carries the
// key because a rule listing a group needs to know which subject each answer
// belongs to — values alone would give it the group's contents with no way to
// tell them apart. One object per line rather than one array so a hook can pipe
// the output through the line-oriented tools it already uses, and so a large
// group does not have to be held whole to be read.
//
// --owner names a DIFFERENT guardrail whose list to read. Without it, list is
// the calling guardrail's own entries, as get and set always are. With it, list
// is the named owner's entries — the one place a rule may read across the
// per-guardrail boundary, and only for list, only to read. Its use is a gate
// that cross-references the registry a sibling context accumulated: the context
// logs each subject under its own guardrail, and the gate reads the group back
// by naming that context as --owner.
//
// Naming another guardrail is safe here for the reason get and set still refuse
// it is unnecessary: the isolation guarded against a rule reading another's
// state and then depending on WHEN that rule ran, and that ordering is the
// caller's to establish with `require: [{context: <owner>}]`, which guarantees
// the owner entered this cycle before this check runs. The read returns the
// entries; `require` makes them current. --owner does not open get or set, so a
// rule still cannot read another's single key nor write into another's
// keyspace.
//
// It reads across guardrails, never across sessions or workspaces: the database
// is the calling session's own, resolved from the engine-set session and
// workspace, and --owner selects only which guardrail's rows within it are
// returned. Being in a hook is still required — outside one there is no
// guardrail in scope and no session to resolve a database from, so a bare CLI
// call still errors rather than reading anything, --owner or not.
func newSessionStateListCmd() *cobra.Command {
	var owner string

	cmd := &cobra.Command{
		Use:   "list [prefix]",
		Short: "The entries a guardrail stored under a prefix (this one, or --owner's)",
		Long: `The entries this guardrail stored under a prefix.

One JSON object per line, each with "key" and "value". No prefix means
everything this guardrail has stored.

The value is the text that was stored, carried as a JSON string, so reading it
back means asking for it raw:

  sr-session state list pending/ | jq -r .value

Without -r the value arrives escaped — a stored JSON document comes back as a
quoted string rather than as an object. To work with each value as JSON:

  sr-session state list pending/ | jq -r .value | jq .done

--owner <guardrail> reads a DIFFERENT guardrail's entries instead of this one's.
It is the one read that crosses the per-guardrail boundary, and it is read-only:
get and set stay this guardrail's own. Its use is a gate that reads the registry
a sibling context accumulated —

  sr-session state list --owner scanner-declared scanner:

— where that context wrote each subject under its own name and the gate reads
the group back by naming it.

Reading another guardrail's list does not tell you WHEN it wrote: a gate that
depends on the owner having run THIS cycle declares require: [{context: <owner>}]
on it, which holds the check until the owner has entered. The read supplies the
entries; require supplies the ordering. --owner reads across guardrails only,
within this same session and workspace — it selects which guardrail's rows to
return, never another session's or another tree's.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var prefix string
			if len(args) == 1 {
				prefix = args[0]
			}

			store, guardrail, err := openSessionState()
			if err != nil {
				return err
			}
			defer store.Close()

			// The database openSessionState resolved is the calling session's
			// own, keyed by the engine-set session and workspace. --owner only
			// changes which guardrail's rows within it are read, so the cross-
			// guardrail read cannot reach another session's or workspace's state
			// — there is no argument here that names the database.
			entries, err := listEntries(store, guardrail, owner, prefix)
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

	cmd.Flags().StringVar(&owner, "owner", "",
		"read this named guardrail's entries instead of the calling guardrail's "+
			"(read-only, same session; establish ordering with require: [{context}])")
	return cmd
}

// listEntries reads the caller's own list, or a named owner's when owner is set.
//
// The split is by which guardrail's rows to read, and nothing else: same store,
// same session database, same prefix semantics. An empty owner is the ordinary
// caller-scoped read; a named owner is the cross-guardrail read, which the store
// offers for list alone.
func listEntries(store sessionstate.Store, guardrail, owner, prefix string) ([]sessionstate.Entry, error) {
	if owner != "" {
		return store.ListStateOwned(owner, prefix)
	}
	return store.ListState(guardrail, prefix)
}

// stateLine is one entry as `list` prints it.
type stateLine struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
