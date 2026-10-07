package checkrun

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/checkcache"
	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/guardrail"
)

// Why a stored verdict could not be carried across a key change (MigrationStats reasons).
const (
	skipRuleGone       = "rule no longer declared"
	skipUnreachable    = "commits unreachable"
	skipSubjectGone    = "subject no longer in the range"
	skipNothingMatched = "nothing selected in the range"
	skipNotRebuildable = "range could not be rebuilt"
)

// RebuildKeys is the checkcache.Rebuild of this engine: it carries the verdicts of an older
// key schema into the current one WITHOUT judging anything.
//
// Every stored run records its rule, its base and head, and its per-subject checks, and a
// guard's key is a pure function of (rule, subject, the range's file contents, the fingerprint
// the rule's `subjects:` script gives the subject at that range). So for each stored PASS of a
// guard it rebuilds the subject's inputs at the run's recorded range exactly as `run` does
// (the changeset from git, then the rule's `subjects:` script as it is in the head tree), and
// keys the same verdict under the current schema's fingerprint. A pass whose commits are
// unreachable, whose rule is no longer declared or whose subject no longer appears is skipped
// and counted; the run itself is history either way. No judge is asked, no model is called, and
// no citation is resolved against a transcript.
//
// The rebuild is per (rule, range), so a rule's `subjects:` script runs once per range however
// many runs and subjects were stored for it. It does not depend on what the old key held, so a
// future key change reuses it as it is: bump the schema directory and this derives the keys.
func RebuildKeys(root string, guards []declaration.FileGuard) checkcache.Rebuild {
	return func(old []checkcache.Run) ([]checkcache.Run, checkcache.MigrationStats, error) {
		return rekeyRuns(root, guards, old)
	}
}

type rekeyGroup struct{ rule, base, head string }

type rekeyRef struct{ run, check int }

func rekeyRuns(root string, guards []declaration.FileGuard, old []checkcache.Run) ([]checkcache.Run, checkcache.MigrationStats, error) {
	var st checkcache.MigrationStats
	byRule := map[string]declaration.FileGuard{}
	for _, g := range guards {
		byRule[g.Qualified()] = g
	}
	out := make([]checkcache.Run, len(old))
	copy(out, old)
	groups := map[rekeyGroup][]rekeyRef{}
	for i, r := range old {
		for j, c := range r.Checks {
			if c.Kind == guardKind && c.Status == checkstore.StatusPass && c.Fingerprint != "" {
				k := rekeyGroup{r.Rule, r.BaseRef, r.HeadRef}
				groups[k] = append(groups[k], rekeyRef{i, j})
			}
		}
	}
	keys := make([]rekeyGroup, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.rule != b.rule {
			return a.rule < b.rule
		}
		if a.base != b.base {
			return a.base < b.base
		}
		return a.head < b.head
	})
	owned := map[int]bool{}
	for _, k := range keys {
		fps, reason := rangeFingerprints(root, byRule, k)
		for _, ref := range groups[k] {
			c := old[ref.run].Checks[ref.check]
			fp, ok := fps[c.Subject]
			switch {
			case reason != "":
				st.Skip(reason)
			case !ok:
				st.Skip(skipSubjectGone)
			default:
				if !owned[ref.run] { // never edit the caller's run
					out[ref.run].Checks = append([]checkcache.Check(nil), old[ref.run].Checks...)
					owned[ref.run] = true
				}
				out[ref.run].Checks[ref.check].Fingerprint = fp
				st.Migrated++
			}
		}
	}
	return out, st, nil
}

// rangeFingerprints is the guard fingerprint of every subject of a rule over a recorded range,
// as `run` computes them: by subject id. A non-empty reason says the range yields none.
func rangeFingerprints(root string, rules map[string]declaration.FileGuard, k rekeyGroup) (map[string]string, string) {
	g, ok := rules[k.rule]
	if !ok {
		return nil, skipRuleGone
	}
	if !revisionExists(root, k.base) || !revisionExists(root, k.head) {
		return nil, skipUnreachable
	}
	match, err := guardrail.CompileFileMatch(g.Match)
	if err != nil {
		return nil, skipNotRebuildable
	}
	r := gitrepo.Range{Base: k.base, Head: k.head}
	cs, err := changeset.Build(root, r, changeset.Options{
		Deletions: changeset.DeletionMode(g.Deletions),
		Scan:      Markers,
		Select:    Selector(match),
	})
	if err != nil {
		return nil, skipNotRebuildable
	}
	if len(cs.Files) == 0 {
		return nil, skipNothingMatched
	}
	subjects := []changeset.Subject{changeset.Whole(cs)}
	if g.Subjects != "" {
		subs, reason := rangeSubjects(root, g, r, cs)
		if reason != "" {
			return nil, reason
		}
		subjects = subs
	}
	fps := make(map[string]string, len(subjects))
	for _, sub := range subjects {
		fps[sub.ID] = guardKey(changeset.NewPayload(cs, sub, ""))
	}
	return fps, ""
}

// rangeSubjects runs the rule's `subjects:` script over the range, from the rule's folder as it
// is in the head tree (a read-only checkout of head, like `run`'s), and returns its subjects.
func rangeSubjects(root string, g declaration.FileGuard, r gitrepo.Range, cs changeset.Changeset) ([]changeset.Subject, string) {
	ev := &changesetEvaluation{errw: io.Discard, root: root, params: Params{Root: root}, rng: r}
	ev.snapshots.Lock()
	tree, err := gitrepo.AddSnapshot(root, "", r.Head)
	ev.snapshots.Unlock()
	if err != nil {
		return nil, skipNotRebuildable
	}
	defer func() {
		ev.snapshots.Lock()
		defer ev.snapshots.Unlock()
		_ = tree.Remove()
	}()
	// The script as the head tree has it, when the rule lives in the repository.
	if rel, err := filepath.Rel(root, g.Dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		g.Dir = filepath.Join(tree.Path, rel)
		if _, err := os.Stat(filepath.Join(g.Dir, g.Subjects)); err != nil {
			return nil, skipRuleGone
		}
	}
	subs, err := ev.guardSubjects(g, r, cs, tree.Path)
	if err != nil {
		return nil, skipNotRebuildable
	}
	return subs, ""
}

// revisionExists says rev names a commit of the repository (the empty tree, a range's base
// before the first commit, always exists).
func revisionExists(root, rev string) bool {
	if rev == gitrepo.EmptyTree {
		return true
	}
	if rev == "" {
		return false
	}
	return exec.Command("git", "-C", root, "cat-file", "-e", rev+"^{commit}").Run() == nil
}

// migrationLine is the one line a migration reports.
func migrationLine(st checkcache.MigrationStats) string {
	return fmt.Sprintf("sloprail: the stored verdicts were re-keyed without judging again: %s\n", st)
}
