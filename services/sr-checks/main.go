// Command sr-checks judges a project's file-guards over an explicit range of commits, and
// shows what they concluded. It is its own STANDALONE binary — one binary per command, with
// the root `sr` proxying to it (`sr checks run` is `sr-checks run`).
//
//	sr-checks run    --base <rev> --head <rev>   run what has no stored verdict (scripts, judges, requirements); writes the results
//	sr-checks verify --base <rev> --head <rev>   only reads stored verdicts, runs nothing, writes nothing; exit 1 when red
//	sr-checks show   --base <rev> --head <rev>   each subject's latest result, without a verdict
//	sr-checks changeset --rule X --base --head   what a rule would be handed, without running it
//	sr-checks staged --needs citation            the staged files a commit must cite; read-only
//
// Nothing here tracks a session, a branch or what was judged before: the caller states the
// range, and a guard's verdict over a subject is keyed by the rule, its definition, the
// subject and a fingerprint of the content it was given. Results live on the orphan branch
// `sloprail/checks` (see internal/checkcache), so another clone or CI finds them.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

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
		Short: "Judge the file-guards over an explicit commit range and show the results",
		Long: `Judge the file-guards over an explicit range of commits.

  sr-checks run    --base <rev> --head <rev>   run what has no stored verdict (script, judge, requirement); writes the results
  sr-checks verify --base <rev> --head <rev>   only reads stored verdicts: executes nothing, writes nothing; exit 1 when anything fails or is unjudged
  sr-checks show   --base <rev> --head <rev>   each subject's latest result, without a verdict
  sr-checks changeset --rule <name> --base <rev> --head <rev>   what a rule would be handed, without running it
  sr-checks default-base --head <rev>          the sha a range over head starts at: its merge base with the default branch
  sr-checks staged --needs citation [--amend]  the staged files a commit must cite (what a file-guard's require: citation will want of it)

The range is merge-base(--base, --head)..--head. --base and --head are required: the caller
states the range. EVERY check is cached the same way: a guard's verdict over a subject is keyed by
the rule hash (which covers its scripts and templates), the subject, the content of the subject's
files, the citation quotes (require: citation rules) and the subject's fingerprint from the rule's
subjects script, when it has one — never by a commit, session or agent — so the same content after
a rebase, a squash or a revert is a cache hit, and so is the same content another clone already
judged. ` + "`run`" + ` runs nothing on a hit (a stored fail is replayed); ` + "`verify`" + ` never executes a script or a judge: a key with
no stored verdict is "not judged yet". Results are kept on the orphan branch sloprail/checks, pushed to
origin by ` + "`run`" + ` and read from it by ` + "`verify`" + `.`,
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newRunCmd(), newVerifyCmd(), newShowCmd(), newChangesetCmd(), newDefaultBaseCmd(), newStagedCmd())
	return root
}
