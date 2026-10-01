package repo

import (
	"io/fs"
	"os"
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
