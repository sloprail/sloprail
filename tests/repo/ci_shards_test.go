package repo

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every package under tests/ that holds a test runs in CI. The e2e suite is split
// into shards (the Makefile's test-e2e-shard cases, one matrix leg each in
// .github/workflows/test.yml); a test package named in no shard — or a shard the
// workflow does not run — is dropped from CI silently, which is how
// tests/e2e/changeset and tests/e2e/checks went unrun for a while. This fails
// the moment a tests/ package is in no shard, or the Makefile and the workflow
// disagree on the shard names.
func TestEveryTestPackageIsInACIShard(t *testing.T) {
	root := repoRoot(t)
	makefile := readRepoFile(t, root, "Makefile")

	// The paths a shard (or test-unit) hands to `go test`.
	covered := regexp.MustCompile(`\./tests/[A-Za-z0-9_./-]*`).FindAllString(makefile, -1)
	if len(covered) < 10 {
		t.Fatalf("found only %d ./tests/ paths in the Makefile — the parse is wrong", len(covered))
	}
	// The examples shards are not listed in the Makefile: scripts/examples-shard.sh
	// discovers them. Run it for every shard the Makefile invokes, require the
	// slices to be disjoint and complete, and count what it prints as covered.
	covered = append(covered, exampleShardPackages(t, root, makefile)...)

	var missing []string
	err := filepath.WalkDir(filepath.Join(root, "tests"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkg := "./" + filepath.ToSlash(rel)
		for _, c := range covered {
			if inShardPath(pkg, c) {
				return nil
			}
		}
		if !slicesContains(missing, pkg) {
			missing = append(missing, pkg)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(missing)
	for _, pkg := range missing {
		t.Errorf("%s is in no CI shard: add it to a test-e2e-shard case in the Makefile (or to test-unit)", pkg)
	}
}

// exampleShardPackages runs scripts/examples-shard.sh for each `INDEX COUNT`
// the Makefile calls it with and returns the packages (as ./tests/... paths) the
// shards cover. It fails if the indices are not exactly 1..COUNT, if two shards
// share a package, or if the union is not every package `go list` finds under
// tests/e2e/examples — so an example in no shard fails here.
func exampleShardPackages(t *testing.T, root, makefile string) []string {
	t.Helper()
	calls := regexp.MustCompile(`scripts/examples-shard\.sh (\d+) (\d+)`).FindAllStringSubmatch(makefile, -1)
	if len(calls) == 0 {
		t.Fatal("the Makefile never calls scripts/examples-shard.sh — the examples are in no shard")
	}
	count := calls[0][2]
	seenIdx := map[string]bool{}
	for _, c := range calls {
		if c[2] != count {
			t.Fatalf("the Makefile calls examples-shard.sh with different shard counts (%s and %s)", count, c[2])
		}
		seenIdx[c[1]] = true
	}
	if len(seenIdx) != len(calls) || len(calls) != atoiOrFail(t, count) {
		t.Fatalf("the Makefile calls examples-shard.sh %d times for %d distinct indices; want one call per index 1..%s", len(calls), len(seenIdx), count)
	}

	list := func(args ...string) []string {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return strings.Fields(string(out))
	}
	all := list("go", "list", "./tests/e2e/examples/...")
	owner := map[string]string{}
	var covered []string
	for _, c := range calls {
		for _, p := range list("scripts/examples-shard.sh", c[1], c[2]) {
			if prev, dup := owner[p]; dup {
				t.Errorf("%s is in example shards %s and %s", p, prev, c[1])
			}
			owner[p] = c[1]
			covered = append(covered, "./"+strings.TrimPrefix(p, "github.com/sloprail/sloprail/"))
		}
	}
	for _, p := range all {
		if _, ok := owner[p]; !ok {
			t.Errorf("%s is in no example shard (scripts/examples-shard.sh)", p)
		}
	}
	return covered
}

func atoiOrFail(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

// inShardPath reports whether the go package pkg is named by the shard path p
// (`./tests/e2e/x/...` covers everything below x; a bare directory covers itself).
func inShardPath(pkg, p string) bool {
	if base, ok := strings.CutSuffix(p, "/..."); ok {
		return pkg == base || strings.HasPrefix(pkg, base+"/")
	}
	return pkg == strings.TrimSuffix(p, "/")
}

// The workflow runs exactly the shards the Makefile defines.
func TestWorkflowMatrixMatchesMakefileShards(t *testing.T) {
	root := repoRoot(t)
	makefile := readRepoFile(t, root, "Makefile")
	workflow := readRepoFile(t, root, ".github/workflows/test.yml")

	m := regexp.MustCompile(`shard:\s*\[([^\]]*)\]`).FindStringSubmatch(workflow)
	if m == nil {
		t.Fatal("no `shard: [...]` matrix in .github/workflows/test.yml")
	}
	var matrix []string
	for _, s := range strings.Split(m[1], ",") {
		matrix = append(matrix, strings.TrimSpace(s))
	}

	// Cases of test-e2e-shard: lines like `  session)  go test ...` up to its esac.
	start := strings.Index(makefile, "test-e2e-shard: mock")
	end := strings.Index(makefile[start:], "esac")
	if start < 0 || end < 0 {
		t.Fatal("test-e2e-shard not found in the Makefile")
	}
	var cases []string
	for _, c := range regexp.MustCompile(`(?m)^\t  ([a-z0-9_]+)\)`).FindAllStringSubmatch(makefile[start:start+end], -1) {
		cases = append(cases, c[1])
	}

	sort.Strings(matrix)
	sort.Strings(cases)
	if strings.Join(matrix, ",") != strings.Join(cases, ",") {
		t.Errorf("the workflow matrix runs shards %v but the Makefile defines %v", matrix, cases)
	}
}

func readRepoFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func slicesContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
