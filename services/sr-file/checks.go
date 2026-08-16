package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/fingerprint"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/statedir"
)

// newChecksCmd builds `sr-file checks`, one plugin's register of what it has
// judged and what it is still refusing.
//
// # Why a plugin needs this at all
//
// A refusal has to OUTLIVE THE TURN. A rule that objects once and forgets is not
// a rule: the agent is told no, the turn ends, and the next turn starts from a
// clean sheet with the file still wrong. What makes a refusal mean anything is
// that it keeps being reported until the thing is fixed.
//
// That is not expressible as a flat key-value lookup, which is why this exists
// rather than plugins keying their own strings into `sr-session state`. "Is this
// exact content already judged" is a point lookup and would be fine. "Which
// files am I still refusing" is not: it is the LATEST verdict per path, and
// reading it any other way gets the rule backwards in one of two directions —
// filtering on refusals alone keeps reporting a file that was refused at one
// content and has since passed at another, so a fix never ends the reporting;
// taking the last write globally reports only whichever path was touched last.
//
// # Every plugin has its OWN register
//
// The owner is part of the key, so two plugins judging one file hold two
// verdicts and neither can clear the other's. A newly installed plugin judges
// everything afresh rather than inheriting somebody else's pass, and one plugin
// refusing a file says nothing about another's opinion of it.
//
// The owner is named by the caller and nothing enforces it. That is stated
// rather than dressed up: no engine stands between a plugin and this store, so
// separate registers rest on plugins naming themselves honestly.
func newChecksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "checks --owner <name> <command>",
		Short: "One plugin's register of judged files and outstanding refusals",
		Long: "One plugin's register of judged files and outstanding refusals.\n\n" +
			"A verdict is remembered per (path, owner, CONTENT), so a file edited away and\n" +
			"edited back is the same question already answered, while the same content arriving\n" +
			"at a new path is a new one.\n\n" +
			"--owner is required and says whose register this is. Separate plugins keep separate\n" +
			"records; nothing stops one naming another, and nothing tries to.\n\n" +
			"The session and workspace come from the environment (SR_SESSION_ID, SR_WORKSPACE),\n" +
			"because those are facts about where the plugin is running rather than names it picks.",
	}
	cmd.PersistentFlags().String("owner", "", "Whose register this is, e.g. the plugin's name (required)")
	cmd.AddCommand(newChecksSkipCmd(), newChecksRecordCmd(), newChecksOutstandingCmd())
	return cmd
}

// openChecks opens the calling session's store under the named owner.
func openChecks(cmd *cobra.Command) (sessionstate.Store, string, error) {
	owner, _ := cmd.Flags().GetString("owner")
	if owner == "" {
		return nil, "", fmt.Errorf("sr-file checks: no owner — pass --owner to say whose register this is")
	}
	path, err := statedir.SessionDBPath(os.Getenv(statedir.WorkspaceEnv), os.Getenv(statedir.SessionEnv))
	if err != nil {
		return nil, "", err
	}
	store, err := sessionstate.Open(path)
	if err != nil {
		return nil, "", err
	}
	return store, owner, nil
}

// newChecksSkipCmd is the cheap gate a plugin runs before doing expensive work.
func newChecksSkipCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "skip <path>",
		Short: "Exit 0 if this owner has already passed the file's current content",
		Long: "Exit 0 if this owner has already passed the file's CURRENT content, 1 otherwise.\n\n" +
			"Written as a status rather than as output so it reads as a guard in a shell:\n\n" +
			"  sr-file checks skip --owner rubrics \"$path\" && continue\n\n" +
			"Only content this owner PASSED is skippable. Content it refused is not — the file\n" +
			"is still wrong and must be judged again, which is what lets a fix be noticed.\n" +
			"Content never judged is not skippable either, and the two are deliberately the\n" +
			"same answer here: both mean there is work to do.\n\n" +
			"The fingerprint is computed from the file on disk rather than passed in, so two\n" +
			"plugins cannot disagree about what 'the same content' means.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, owner, err := openChecks(cmd)
			if err != nil {
				return err
			}
			defer store.Close()

			rel, abs, err := resolvePath(args[0])
			if err != nil {
				return err
			}
			fp, err := fingerprint.OfFile(abs)
			if err != nil {
				// A file that cannot be read is not skippable. Reported rather
				// than answered "no" silently: a plugin that meets this has a
				// path problem, not a judging decision to make.
				return fmt.Errorf("sr-file checks skip: %s: %w", rel, err)
			}
			ok, err := store.Skippable(rel, owner, fp)
			if err != nil {
				return err
			}
			if !ok {
				return errNotSkippable
			}
			return nil
		},
	}
}

// errNotSkippable carries "no" as a status with nothing printed. Not an error
// the caller reports — it is the ordinary answer on the first sight of a file.
var errNotSkippable = fmt.Errorf("")

// newChecksRecordCmd stores a verdict.
func newChecksRecordCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "record <path>",
		Short: "Record this owner's verdict on the file's current content",
		Long: "Record this owner's verdict on the file's CURRENT content.\n\n" +
			"  sr-file checks record --owner rubrics --passed \"$path\"\n" +
			"  sr-file checks record --owner rubrics --passed=false \"$path\"\n\n" +
			"A refusal recorded here is what `outstanding` reports afterwards, and what makes\n" +
			"the refusal survive the turn that produced it. A plugin that refuses without\n" +
			"recording has objected once and forgotten — the next turn starts clean with the\n" +
			"file still wrong.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, owner, err := openChecks(cmd)
			if err != nil {
				return err
			}
			defer store.Close()

			passed, _ := cmd.Flags().GetBool("passed")
			rel, abs, err := resolvePath(args[0])
			if err != nil {
				return err
			}
			fp, err := fingerprint.OfFile(abs)
			if err != nil {
				return fmt.Errorf("sr-file checks record: %s: %w", rel, err)
			}
			return store.RecordFileCheck(rel, owner, sessionstate.Verdict{
				Fingerprint: fp,
				Passed:      passed,
			})
		},
	}
	cmd.Flags().Bool("passed", true, "Whether the content satisfied this owner's check")
	return cmd
}

// newChecksOutstandingCmd reports what this owner is still refusing.
func newChecksOutstandingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "outstanding",
		Short: "Report the files this owner has refused and not since passed",
		Long: "Report the files this owner has refused and not since passed, as JSONL.\n\n" +
			"  {\"path\":\"a.md\",\"fingerprint\":\"...\"}\n\n" +
			"This is the reader that makes a retained refusal mean something. A plugin runs it\n" +
			"at the end of a turn and refuses the turn while anything comes back, so an\n" +
			"unfixed file is re-reported rather than scrolling away.\n\n" +
			"A path is outstanding while this owner's MOST RECENT verdict on it is a refusal.\n" +
			"Judged again at new content and passed, it falls out; judged again and refused, it\n" +
			"stays with the new content named. Reading it as 'every refusal ever' would pin a\n" +
			"file forever after one bad version, so a fix could never end the reporting.\n\n" +
			"Exit status is 0 whether or not anything is outstanding — the rows are the answer,\n" +
			"and a plugin decides for itself what to do about them.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, owner, err := openChecks(cmd)
			if err != nil {
				return err
			}
			defer store.Close()

			refusals, err := store.OutstandingRefusals()
			if err != nil {
				return err
			}
			for _, r := range refusals {
				// Filtered to THIS owner. The store answers for every owner at
				// once because the engine used to want that; a plugin must see
				// only its own register, or one plugin's refusal would make
				// another's turn fail.
				if r.Guardrail != owner {
					continue
				}
				line, err := json.Marshal(map[string]any{
					"path":        r.Path,
					"fingerprint": r.Fingerprint,
				})
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(line))
			}
			return nil
		},
	}
}

// resolvePath returns a path both workspace-relative (how a verdict is keyed)
// and absolute (how the file is read).
//
// Keyed RELATIVE so a register survives the tree being checked out elsewhere,
// and so it matches the spelling `sr-file changes` reports — a plugin piping one
// into the other must not find that the two disagree about what the path is.
func resolvePath(p string) (rel, abs string, err error) {
	root := os.Getenv(statedir.WorkspaceEnv)
	if root == "" {
		root, err = os.Getwd()
		if err != nil {
			return "", "", err
		}
	}
	if filepath.IsAbs(p) {
		rel, err = filepath.Rel(root, p)
		if err != nil {
			return "", "", err
		}
		return rel, p, nil
	}
	return p, filepath.Join(root, p), nil
}
