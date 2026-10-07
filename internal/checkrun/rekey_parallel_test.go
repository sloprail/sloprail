package checkrun

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/checkcache"
	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/guardrail"
)

// sharedFixture is a repository with four rules over a history in which many ranges end at the
// same few heads: two rules with a `subjects:` script whose fingerprint is read from the head
// tree (so a checkout shared across heads would be caught), and two with none (one of which
// takes deletions), which the migration builds without reading a file.
type sharedFixture struct {
	repo    string
	commits []string
	guards  []declaration.FileGuard
}

func newSharedFixture(t *testing.T) *sharedFixture {
	t.Helper()
	t.Setenv("SR_CHECKS_JUDGE_MOCKS", "{}")
	repo := initRepo(t)
	gitT(t, repo, "config", "gc.auto", "0")
	f := &sharedFixture{repo: repo}
	f.commits = append(f.commits, commitFile(t, repo, "seed.txt", "seed"))

	rule := func(name, match, deletions, subjects string) declaration.FileGuard {
		dir := filepath.Join(repo, ".sloprail", "file-guard", name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		g := declaration.FileGuard{Name: name, Match: match, Dir: dir, Deletions: declaration.Deletions(deletions)}
		if subjects != "" {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "subjects.sh"), []byte(subjects), 0o755))
			g.Subjects = "./subjects.sh"
		}
		return g
	}
	// One subject per docs file in the changeset, fingerprinted with the version the HEAD tree
	// holds, plus (for "docs-all") a subject that is not in the changeset at all.
	perFile := "#!/bin/sh\ncat >/dev/null\nv=$(cat \"$SR_TREE/version.txt\")\nprintf '['\nsep=''\nfor f in docs/a.md docs/b.md; do\n" +
		"  if git -C \"$SR_TREE\" diff --quiet \"$SR_BASE\" \"$SR_HEAD\" -- \"$f\"; then continue; fi\n" +
		"  printf '%s{\"id\":\"%s\",\"files\":[\"%s\"],\"fingerprint\":\"%s\"}' \"$sep\" \"$f\" \"$f\" \"$v\"; sep=','\ndone\nprintf ']'\n"
	oneAPI := "#!/bin/sh\ncat >/dev/null\nv=$(cat \"$SR_TREE/version.txt\")\necho \"[{\\\"id\\\":\\\"api\\\",\\\"files\\\":[\\\"docs/a.md\\\"],\\\"fingerprint\\\":\\\"api-$v\\\"}]\"\n"
	f.guards = []declaration.FileGuard{
		rule("docs-files", "docs/**", "", perFile),
		rule("docs-api", "docs/a.md", "", oneAPI),
		rule("notes", "notes/**", "", ""),
		rule("everything", "**", "include", ""),
		rule("empty", "empty/**", "", ""),
		rule("docs-md", "docs/**", "", ""),
		rule("a-only", "docs/a.md", "", ""),
		rule("txt", "*.txt", "", ""),
		rule("version", "version.txt", "", ""),
		rule("notes-again", "notes/**", "", ""),
		rule("everything-again", "**", "include", ""),
	}
	f.commits = append(f.commits, commitFile(t, repo, "version.txt", "v0")) // and the rules, committed with it

	step := func(version string, files map[string]string, remove ...string) {
		files["version.txt"] = version
		for name, body := range files {
			require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(repo, name)), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644))
		}
		for _, name := range remove {
			runGit(t, repo, "rm", "-q", name)
		}
		runGit(t, repo, "add", "-A")
		runGit(t, repo, "commit", "-m", "step "+version)
		f.commits = append(f.commits, runGit(t, repo, "rev-parse", "HEAD"))
	}
	step("v1", map[string]string{"docs/a.md": "a1", "notes/n.md": "n1"})
	step("v2", map[string]string{"docs/b.md": "b1"})
	step("v3", map[string]string{"docs/a.md": "a2", "notes/m.md": "m1"})
	step("v4", map[string]string{"docs/b.md": "b2"}, "notes/n.md")
	step("v5", map[string]string{"docs/a.md": "a3"})
	return f
}

// runs stores passing guard verdicts, under keys that are nothing the new engine computes. Every
// (rule, base, head) has an old pass first (all superseded in the end), then each rule's NEWEST
// pass is over its own range, the heads shared among the rules: those are what is carried.
func (f *sharedFixture) runs() []checkcache.Run {
	var runs []checkcache.Run
	bases, heads := f.commits[:4], f.commits[4:]
	missing := "0123456789abcdef0123456789abcdef01234567"
	add := func(g declaration.FileGuard, base, head string, subjects ...string) {
		id := fmt.Sprintf("r%03d", len(runs))
		r := checkcache.Run{ID: id, RunAt: fmt.Sprintf("2026-10-01T10:%02d:%02d.000Z", len(runs)/60, len(runs)%60), Rule: g.Qualified(), RuleHash: "old",
			BaseRef: base, HeadRef: head, Complete: true}
		for _, s := range subjects {
			r.Checks = append(r.Checks, checkcache.Check{Subject: s, Kind: guardKind, Status: checkstore.StatusPass, Fingerprint: "old-" + id + "-" + s})
		}
		runs = append(runs, r)
	}
	subjectsOf := func(g declaration.FileGuard) []string {
		switch g.Name {
		case "docs-files":
			return []string{"docs/a.md", "docs/b.md", "docs/missing.md"}
		case "docs-api":
			return []string{"api"}
		}
		return []string{"changeset"}
	}
	for _, g := range f.guards {
		for _, head := range heads {
			for _, base := range bases {
				add(g, base, head, subjectsOf(g)...)
			}
		}
	}
	for i, g := range f.guards {
		base, head := bases[i%len(bases)], heads[i%len(heads)]
		switch g.Name {
		case "docs-files": // one newest pass per subject, over different ranges
			add(g, bases[0], heads[0], "docs/a.md")
			add(g, bases[1], heads[1], "docs/b.md")
			add(g, bases[2], heads[2], "docs/missing.md")
		case "txt":
			add(g, bases[0], missing, subjectsOf(g)...) // unreachable
		default:
			add(g, base, head, subjectsOf(g)...)
		}
	}
	// A rule that is no longer declared, over a shared head.
	add(declaration.FileGuard{Name: "removed"}, bases[0], heads[0], "changeset")
	return runs
}

// The migration builds each head's checkout once, shares changesets between rules and runs
// ranges in parallel; none of it may change a result. The reference is the plain way: every
// (rule, base, head) rebuilt on its own, one after the other, as the migration first did.
func TestRekey_ParallelAndSharedEqualsSerial(t *testing.T) {
	f := newSharedFixture(t)
	old := f.runs()
	byRule := map[string]declaration.FileGuard{}
	for _, g := range f.guards {
		byRule[g.Qualified()] = g
	}

	// The serial reference: only the newest pass of each (rule, subject) is carried, the others
	// are skipped as superseded; each carried one is rebuilt on its own, one after the other.
	newest := map[[2]string]int{}
	for i, r := range old { // runs are stored oldest first, so the last one seen is the newest
		for _, c := range r.Checks {
			newest[[2]string{r.Rule, c.Subject}] = i
		}
	}
	wantFP := map[[2]int]string{}
	var want checkcache.MigrationStats
	for i, r := range old {
		fps, reason := rangeFingerprints(f.repo, byRule, rekeyGroup{r.Rule, r.BaseRef, r.HeadRef})
		for j, c := range r.Checks {
			fp, ok := fps[c.Subject]
			switch {
			case newest[[2]string{r.Rule, c.Subject}] != i:
				want.Skip(skipSuperseded)
			case reason != "":
				want.Skip(reason)
			case !ok:
				want.Skip(skipSubjectGone)
			default:
				wantFP[[2]int{i, j}] = fp
				want.Migrated++
			}
		}
	}
	require.Greater(t, want.Migrated, 8, "the fixture carries many verdicts")
	require.Greater(t, want.Reasons[skipSuperseded], 40, "most are superseded")
	require.Len(t, want.Reasons, 5, "and every reason a verdict is skipped: %v", want.Reasons)

	before := fmt.Sprint(old)
	var progress bytes.Buffer
	rekeyProgress = &progress
	t.Cleanup(func() { rekeyProgress = os.Stderr })
	t.Setenv(StopConcurrencyEnv, "8")

	got, st, err := rekeyRuns(f.repo, f.guards, old)
	require.NoError(t, err)
	assert.Equal(t, want.Migrated, st.Migrated)
	assert.Equal(t, want.Skipped, st.Skipped)
	assert.Equal(t, want.Reasons, st.Reasons)
	assert.Equal(t, want.String(), st.String())
	for i, r := range got {
		for j, c := range r.Checks {
			if fp, ok := wantFP[[2]int{i, j}]; ok {
				assert.Equal(t, fp, c.Fingerprint, "run %s subject %s", r.ID, c.Subject)
				assert.NotEmpty(t, c.FilesPart, "the migrated record keeps what its key was made of")
				assert.Equal(t, fingerprintOfParts(c.FilesPart, c.SubjectFingerprint), c.Fingerprint, "and the parts give the key back")
			} else {
				assert.Equal(t, old[i].Checks[j].Fingerprint, c.Fingerprint, "a skipped verdict is left as it was (%s %s)", r.ID, c.Subject)
			}
		}
	}
	assert.Equal(t, before, fmt.Sprint(old), "the caller's runs are never edited")

	// Progress goes to the writer, and the last line carries the counts and the time.
	lines := strings.Split(strings.TrimSpace(progress.String()), "\n")
	last := lines[len(lines)-1]
	assert.Contains(t, last, "sloprail: migrating cache keys: ")
	assert.Contains(t, last, fmt.Sprintf("migrated %d, skipped %d", want.Migrated, want.Skipped))
	assert.Contains(t, last, " in ")

	// Every shared checkout is gone: only the repository's own worktree is left registered.
	assert.Equal(t, 1, strings.Count(runGit(t, f.repo, "worktree", "list"), "\n")+1)
}

// The key a lean (no file read) changeset gives equals the one a full changeset gives: the
// verdict key is over paths and git blob ids only, so `run`, `verify` and the migration agree.
func TestRekey_LeanKeysEqualFullKeys(t *testing.T) {
	f := newSharedFixture(t)
	for _, g := range f.guards {
		if g.Subjects != "" || g.Name == "empty" {
			continue
		}
		for _, base := range f.commits[:4] {
			k := rekeyGroup{g.Qualified(), base, f.commits[len(f.commits)-1]}
			lean, reason := rangeFingerprints(f.repo, map[string]declaration.FileGuard{k.rule: g}, k)
			require.Empty(t, reason)

			match, err := guardrail.CompileFileMatch(g.Match)
			require.NoError(t, err)
			cs, err := changeset.Build(f.repo, gitrepo.Range{Base: k.base, Head: k.head}, changeset.Options{
				Deletions: changeset.DeletionMode(g.Deletions), Scan: Markers, Select: Selector(match)})
			require.NoError(t, err)
			require.NotEmpty(t, cs.Files)
			assert.Equal(t, guardKey(changeset.NewPayload(cs, changeset.Whole(cs), "")), lean[changeset.DefaultSubjectID], "%s over %s", g.Name, base[:8])
		}
	}
}
