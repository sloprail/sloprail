package checkrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
)

func citeGuard(t *testing.T, repo, match, when string) declaration.FileGuard {
	t.Helper()
	dir := filepath.Join(repo, ".sloprail", "file-guard", "cited")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	if when != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "when.sh"), []byte(when), 0o755))
	}
	p := declaration.Prerequisite{Citation: &declaration.CitationPrerequisite{SourceTypes: []string{"user"}}}
	if when != "" {
		p.When = "./when.sh"
	}
	return declaration.FileGuard{Name: "cited", Match: match, Dir: dir, Require: []declaration.Prerequisite{p}}
}

func staged(t *testing.T, repo string, amend bool, gs ...declaration.FileGuard) ([]string, error) {
	t.Helper()
	return StagedNeedingCitation(StagedParams{Guards: gs, Root: repo, Amend: amend})
}

func stage(t *testing.T, repo, name, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(repo, filepath.Dir(name)), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644))
	runGit(t, repo, "add", "--", name)
}

func TestStaged_ListsOnlyGuardedStagedFiles(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "seed.txt", "seed")
	stage(t, repo, "docs/b.md", "b")
	stage(t, repo, "docs/a.md", "a")
	stage(t, repo, "src/x.go", "x")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "docs", "unstaged.md"), []byte("u"), 0o644))

	got, err := staged(t, repo, false, citeGuard(t, repo, "docs/**", ""))
	require.NoError(t, err)
	assert.Equal(t, []string{"docs/a.md", "docs/b.md"}, got, "sorted, staged only, guarded only")
}

func TestStaged_NothingGuardedIsEmpty(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "seed.txt", "seed")
	stage(t, repo, "src/x.go", "x")
	got, err := staged(t, repo, false, citeGuard(t, repo, "docs/**", ""))
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestStaged_AGuardWithoutACitationRequirementIsIgnored(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "seed.txt", "seed")
	stage(t, repo, "docs/a.md", "a")
	g := declaration.FileGuard{Name: "plain", Match: "docs/**", Dir: repo, Checks: []declaration.Check{{Script: "./x.sh"}}}
	got, err := staged(t, repo, false, g)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestStaged_WhenWaivesPerFile(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "seed.txt", "seed")
	stage(t, repo, "docs/a.md", "a")
	stage(t, repo, "docs/b.md", "b")
	// exit 1 (waived) for b.md, 0 (applies) for the rest.
	when := "#!/bin/sh\ncat | jq -e '.subject.files[0] == \"docs/b.md\"' >/dev/null && exit 1\nexit 0\n"
	got, err := staged(t, repo, false, citeGuard(t, repo, "docs/**", when))
	require.NoError(t, err)
	assert.Equal(t, []string{"docs/a.md"}, got)
}

func TestStaged_WhenThatCannotDecideApplies(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "seed.txt", "seed")
	stage(t, repo, "docs/a.md", "a")
	got, err := staged(t, repo, false, citeGuard(t, repo, "docs/**", "#!/bin/sh\nexit 7\n"))
	require.NoError(t, err)
	assert.Equal(t, []string{"docs/a.md"}, got)
}

func TestStaged_UnbornHeadReadsAgainstTheEmptyTree(t *testing.T) {
	repo := initRepo(t)
	stage(t, repo, "docs/a.md", "a")
	stage(t, repo, "other.txt", "o")
	got, err := staged(t, repo, false, citeGuard(t, repo, "docs/**", ""))
	require.NoError(t, err)
	assert.Equal(t, []string{"docs/a.md"}, got)
}

func TestStaged_AmendCountsWhatHeadChanged(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "seed.txt", "seed")
	stage(t, repo, "docs/a.md", "a")
	runGit(t, repo, "commit", "-m", "guarded") // HEAD changed a guarded file
	stage(t, repo, "src/x.go", "x")

	plain, err := staged(t, repo, false, citeGuard(t, repo, "docs/**", ""))
	require.NoError(t, err)
	assert.Empty(t, plain, "a new commit adds only the index's change")

	amend, err := staged(t, repo, true, citeGuard(t, repo, "docs/**", ""))
	require.NoError(t, err)
	assert.Equal(t, []string{"docs/a.md"}, amend, "the amended commit replaces HEAD, so HEAD's change is its again")
}

func TestStaged_AmendOfTheRootCommitReadsAgainstTheEmptyTree(t *testing.T) {
	repo := initRepo(t)
	stage(t, repo, "docs/a.md", "a")
	runGit(t, repo, "commit", "-m", "root")
	got, err := staged(t, repo, true, citeGuard(t, repo, "docs/**", ""))
	require.NoError(t, err)
	assert.Equal(t, []string{"docs/a.md"}, got)
}

func TestStaged_AmendWithNoCommitFailsClosed(t *testing.T) {
	repo := initRepo(t)
	stage(t, repo, "docs/a.md", "a")
	_, err := staged(t, repo, true, citeGuard(t, repo, "docs/**", ""))
	assert.Error(t, err)
}

func TestStaged_AMatchThatDoesNotCompileFailsClosed(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "seed.txt", "seed")
	stage(t, repo, "docs/a.md", "a")
	_, err := staged(t, repo, false, citeGuard(t, repo, "path ==", ""))
	assert.Error(t, err)
}

func TestStaged_ReadsTheIndexAndTouchesNothing(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "seed.txt", "seed")
	stage(t, repo, "docs/a.md", "a")
	before := runGit(t, repo, "status", "--porcelain")
	headBefore := runGit(t, repo, "rev-parse", "HEAD")
	_, err := staged(t, repo, false, citeGuard(t, repo, "docs/**", ""))
	require.NoError(t, err)
	assert.Equal(t, before, runGit(t, repo, "status", "--porcelain"))
	assert.Equal(t, headBefore, runGit(t, repo, "rev-parse", "HEAD"))
	assert.Equal(t, "refs/heads/main", runGit(t, repo, "symbolic-ref", "HEAD"))
}

func TestStaged_ReadsTheIndexGitIndexFileNames(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "seed.txt", "seed")
	scratch := filepath.Join(t.TempDir(), "index")
	data, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(scratch, data, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "docs-a.md"), []byte("a"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "docs", "a.md"), []byte("a"), 0o644))
	t.Setenv("GIT_INDEX_FILE", scratch)
	runGit(t, repo, "add", "docs/a.md") // staged in the scratch index only

	got, err := staged(t, repo, false, citeGuard(t, repo, "docs/**", "#!/bin/sh\nexit 0\n"))
	require.NoError(t, err)
	assert.Equal(t, []string{"docs/a.md"}, got)
	t.Setenv("GIT_INDEX_FILE", "")
	os.Unsetenv("GIT_INDEX_FILE")
	assert.Empty(t, runGit(t, repo, "diff", "--cached", "--name-only"), "the real index is untouched")
}

// A second evaluation of the same candidate (the gate asks as `when` and again as `check`) reads the
// `when` verdicts from the cache instead of running the script per subject again.
func TestStaged_WhenVerdictsAreCachedAcrossEvaluations(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "seed.txt", "seed")
	counter := filepath.Join(t.TempDir(), "runs")
	when := "#!/bin/sh\ncat >/dev/null\necho x >> " + counter + "\ncase \"$(cat /dev/null)\" in *) exit 0 ;; esac\n"
	g := citeGuard(t, repo, "docs/**", when)
	for _, n := range []string{"a", "b", "c"} {
		stage(t, repo, "docs/"+n+".md", n)
	}
	first, err := staged(t, repo, false, g)
	require.NoError(t, err)
	b, _ := os.ReadFile(counter)
	assert.Equal(t, 3, strings.Count(string(b), "x"), "one run per subject")
	second, err := staged(t, repo, false, g)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	b, _ = os.ReadFile(counter)
	assert.Equal(t, 3, strings.Count(string(b), "x"), "the second evaluation ran no script")

	stage(t, repo, "docs/d.md", "d")
	_, err = staged(t, repo, false, g)
	require.NoError(t, err)
	b, _ = os.ReadFile(counter)
	assert.Equal(t, 7, strings.Count(string(b), "x"), "a changed candidate is judged afresh")
}
