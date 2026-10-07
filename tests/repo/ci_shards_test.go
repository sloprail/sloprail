package repo

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Every test the repo has runs in CI. The e2e suite is split into shards (the
// Makefile's test-e2e-shard cases, one matrix leg each in
// .github/workflows/test.yml); a test package named in no shard — or a shard the
// workflow does not run — is dropped from CI silently, which is how
// tests/e2e/harness/changeset and tests/e2e/harness/checks went unrun for a while. These tests
// fail the moment
//
//   - a tests/ package is in no shard (a new folder under tests/e2e/**, a new
//     example package),
//   - a test of a package that scripts/e2e-shard.sh slices with -run is in
//     no slice,
//   - a nested Go module (a plugin's tests/ module) is not one that
//     `make test-plugins-e2e` discovers, or
//   - the workflow matrix and the Makefile's shard cases differ.
//
// The checks are functions over file contents so the negative tests below can
// prove each one fires on a fake tree.

func TestEveryTestPackageIsInACIShard(t *testing.T) {
	root := repoRoot(t)
	makefile := readRepoFile(t, root, "Makefile")

	if n := len(shardPaths(makefile)); n < 10 {
		t.Fatalf("found only %d ./tests/ paths in the Makefile — the parse is wrong", n)
	}
	extra, problems := shardScriptCoverage(t, root, makefile)
	problems = append(problems, coverageProblems(t, root, makefile, extra)...)
	for _, p := range problems {
		t.Error(p)
	}
}

// The workflow runs exactly the shards the Makefile defines, and exposes the
// one aggregate check branch protection can require.
func TestWorkflowMatrixMatchesMakefileShards(t *testing.T) {
	root := repoRoot(t)
	for _, p := range matrixProblems(readRepoFile(t, root, "Makefile"), readRepoFile(t, root, ".github/workflows/test.yml")) {
		t.Error(p)
	}
}

// --- negative tests: the guard fires ---

func TestGuardFiresOnAnUnshardedPackage(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	makefile := "test-plugins-e2e:\n\tfind marketplace/plugins -mindepth 2 -maxdepth 2 -type d -name tests\ntest-e2e-shard:\n\t  rest) go test ./tests/e2e/covered/... ;; \\\n"
	write("tests/e2e/covered/a_test.go", "package a\n")
	write("tests/e2e/new_folder/b_test.go", "package b\n")
	write("marketplace/plugins/p/tests/go.mod", "module p\n")
	write("marketplace/plugins/p/tests/x_test.go", "package p\n")
	write("elsewhere/tests/go.mod", "module e\n")

	got := strings.Join(coverageProblems(t, root, makefile, nil), "\n")
	if !strings.Contains(got, "./tests/e2e/new_folder is in no CI shard") {
		t.Errorf("an unsharded package was not reported:\n%s", got)
	}
	if strings.Contains(got, "e2e/covered") {
		t.Errorf("a covered package was reported:\n%s", got)
	}
	if !strings.Contains(got, "elsewhere/tests") {
		t.Errorf("a nested module outside marketplace/plugins/*/tests was not reported:\n%s", got)
	}
	if strings.Contains(got, "marketplace/plugins/p/tests") {
		t.Errorf("a discovered plugin module was reported:\n%s", got)
	}
}

func TestGuardFiresOnAMatrixMakefileMismatch(t *testing.T) {
	makefile := "test-e2e-shard: mock\n\t@case \"$(SHARD)\" in \\\n\t  a) x ;; \\\n\t  b) y ;; \\\n\t  *) exit 2 ;; \\\n\tesac\n"
	wf := func(shards string) string {
		return "strategy:\n  matrix:\n    shard: [" + shards + "]\n  name: e2e (all)\n"
	}
	if got := matrixProblems(makefile, wf("a, b")); len(got) != 0 {
		t.Errorf("matching lists reported: %v", got)
	}
	if got := matrixProblems(makefile, wf("a")); len(got) == 0 {
		t.Error("a Makefile shard missing from the matrix was not reported")
	}
	if got := matrixProblems(makefile, wf("a, b, c")); len(got) == 0 {
		t.Error("a matrix shard missing from the Makefile was not reported")
	}
	if got := matrixProblems(makefile, "shard: [a, b]\n"); len(got) == 0 {
		t.Error("a missing aggregate `e2e (all)` job was not reported")
	}
}

func TestGuardFiresOnATestInNoSlice(t *testing.T) {
	tests := []string{"TestA", "TestB", "TestC"}
	if got := sliceProblems("pkg", tests, []string{"^(TestA|TestB)$", "^(TestC)$"}); len(got) != 0 {
		t.Errorf("a full cover was reported: %v", got)
	}
	if got := sliceProblems("pkg", tests, []string{"^(TestA)$", "^(TestC)$"}); len(got) != 1 || !strings.Contains(got[0], "TestB") {
		t.Errorf("a test in no slice was not reported: %v", got)
	}
	if got := sliceProblems("pkg", tests, []string{"^(TestA|TestB)$", "^(TestB|TestC)$"}); len(got) != 1 || !strings.Contains(got[0], "TestB") {
		t.Errorf("a test in two slices was not reported: %v", got)
	}
}

// --- the checks ---

var shardPathRE = regexp.MustCompile(`\./tests/[A-Za-z0-9_./-]*`)

// shardPaths are the ./tests/ paths the Makefile's cases hand to go test,
// ignoring variable definitions: a path in an unused variable runs nothing.
func shardPaths(makefile string) []string {
	_, text := makeVars(makefile)
	return shardPathRE.FindAllString(text, -1)
}

// coverageProblems reports every package under root/tests holding a test that no
// path in the Makefile (or in extra) covers, and every nested Go module that
// `make test-plugins-e2e` would not discover.
func coverageProblems(t *testing.T, root, makefile string, extra []string) []string {
	t.Helper()
	covered := append(shardPaths(makefile), extra...)
	pluginModule := regexp.MustCompile(`^marketplace/plugins/[^/]+/tests$`)
	var problems []string
	seen := map[string]bool{}
	skip := func(rel string) bool {
		return rel == ".git" || strings.HasPrefix(rel, ".claude") || strings.Contains(rel, "node_modules") || strings.Contains(rel, "testdata")
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if skip(rel) {
				return fs.SkipDir
			}
			return nil
		}
		switch {
		case d.Name() == "go.mod" && rel != "go.mod":
			dir := filepath.ToSlash(filepath.Dir(rel))
			if !pluginModule.MatchString(dir) && !seen["mod:"+dir] {
				seen["mod:"+dir] = true
				problems = append(problems, "nested Go module "+dir+" is run by no shard: only marketplace/plugins/*/tests modules are discovered by `make test-plugins-e2e`")
			}
		case strings.HasPrefix(rel, "tests/") && strings.HasSuffix(d.Name(), "_test.go"):
			pkg := "./" + filepath.ToSlash(filepath.Dir(rel))
			if seen[pkg] {
				return nil
			}
			seen[pkg] = true
			for _, c := range covered {
				if inShardPath(pkg, c) {
					return nil
				}
			}
			problems = append(problems, pkg+" is in no CI shard: add it to a test-e2e-shard case in the Makefile (or to test-unit)")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The plugin shard must really discover modules, or the exemption above lies.
	if !strings.Contains(makefile, "marketplace/plugins -mindepth 2 -maxdepth 2 -type d -name tests") {
		problems = append(problems, "`make test-plugins-e2e` no longer discovers marketplace/plugins/*/tests modules")
	}
	sort.Strings(problems)
	return problems
}

// makeVars returns the Makefile's `NAME := ...` variables (continuation lines
// joined), and the Makefile with those definitions removed.
func makeVars(makefile string) (vars map[string]string, rest string) {
	vars = map[string]string{}
	lines := strings.Split(makefile, "\n")
	var keep []string
	def := regexp.MustCompile(`^([A-Z_][A-Z0-9_]*) :=\s*(.*)$`)
	for i := 0; i < len(lines); i++ {
		m := def.FindStringSubmatch(lines[i])
		if m == nil {
			keep = append(keep, lines[i])
			continue
		}
		val := m[2]
		for strings.HasSuffix(val, `\`) && i+1 < len(lines) {
			i++
			val = strings.TrimSuffix(val, `\`) + " " + strings.TrimSpace(lines[i])
		}
		vars[m[1]] = val
	}
	return vars, strings.Join(keep, "\n")
}

// shardScriptCoverage checks every `scripts/e2e-shard.sh INDEX COUNT run PATTERN...`
// the Makefile makes. Calls with the same COUNT and PATTERNs form one group,
// which must cover indices 1..COUNT exactly once and, run through the script,
// partition exactly the packages `go list PATTERN...` finds. It returns the
// ./tests/... paths the groups cover, plus problems: a missing index, a package
// in two shards or none, a test of a sliced package in no slice.
func shardScriptCoverage(t *testing.T, root, makefile string) (covered, problems []string) {
	t.Helper()
	vars, text := makeVars(makefile)
	calls := regexp.MustCompile(`scripts/e2e-shard\.sh (\d+) (\d+) run ([^;\\\n]*?) ;;`).FindAllStringSubmatch(text, -1)
	if len(calls) == 0 {
		return nil, []string{"the Makefile never calls scripts/e2e-shard.sh — the sharded packages are in no shard"}
	}
	type group struct {
		count    int
		patterns []string
		idx      map[int]bool
	}
	groups := map[string]*group{}
	var order []string
	for _, c := range calls {
		patterns := strings.Fields(regexp.MustCompile(`\$\(([A-Z_][A-Z0-9_]*)\)`).ReplaceAllStringFunc(c[3], func(s string) string {
			return vars[s[2:len(s)-1]]
		}))
		key := c[2] + " " + strings.Join(patterns, " ")
		g := groups[key]
		if g == nil {
			n, _ := strconv.Atoi(c[2])
			g = &group{count: n, patterns: patterns, idx: map[int]bool{}}
			groups[key] = g
			order = append(order, key)
		}
		i, _ := strconv.Atoi(c[1])
		if g.idx[i] {
			problems = append(problems, "the Makefile runs e2e-shard.sh shard "+c[1]+" of "+c[2]+" twice for "+strings.Join(patterns, " "))
		}
		g.idx[i] = true
	}

	run := func(args ...string) []string {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return strings.Fields(strings.ReplaceAll(string(out), "\t", "\x00"))
	}
	// The module path, so import paths can become ./tests/... paths.
	modPath := strings.TrimSpace(run("go", "list", "-m")[0]) + "/"
	for _, key := range order {
		g := groups[key]
		label := strings.Join(g.patterns, " ")
		for i := 1; i <= g.count; i++ {
			if !g.idx[i] {
				problems = append(problems, "no Makefile case runs e2e-shard.sh shard "+strconv.Itoa(i)+" of "+strconv.Itoa(g.count)+" for "+label)
			}
		}
		owner := map[string]int{}
		regexes := map[string][]string{}
		for i := 1; i <= g.count; i++ {
			args := append([]string{"scripts/e2e-shard.sh", strconv.Itoa(i), strconv.Itoa(g.count), "list"}, g.patterns...)
			for _, line := range run(args...) {
				pkg, re, sliced := strings.Cut(line, "\x00")
				if sliced {
					regexes[pkg] = append(regexes[pkg], re)
				} else if prev, dup := owner[pkg]; dup {
					problems = append(problems, pkg+" is in shards "+strconv.Itoa(prev)+" and "+strconv.Itoa(i))
				}
				owner[pkg] = i
				covered = append(covered, "./"+strings.TrimPrefix(pkg, modPath))
			}
		}
		for _, pkg := range run(append([]string{"go", "list"}, g.patterns...)...) {
			if _, ok := owner[pkg]; !ok {
				problems = append(problems, pkg+" is in no shard of scripts/e2e-shard.sh ("+label+")")
			}
		}
		for pkg, res := range regexes {
			dir := filepath.Join(root, strings.TrimPrefix(pkg, modPath))
			problems = append(problems, sliceProblems(pkg, topLevelTests(t, dir), res)...)
		}
	}
	return covered, problems
}

var testFuncRE = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)

// topLevelTests lists the Test functions of the package in dir (TestMain is not one).
func topLevelTests(t *testing.T, dir string) []string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "*_test.go"))
	var names []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range testFuncRE.FindAllStringSubmatch(string(b), -1) {
			if m[1] != "TestMain" {
				names = append(names, m[1])
			}
		}
	}
	return names
}

// sliceProblems reports each test matched by none, or by more than one, of the
// -run regexes a sliced package is run with.
func sliceProblems(pkg string, tests, regexes []string) []string {
	var res []*regexp.Regexp
	for _, r := range regexes {
		res = append(res, regexp.MustCompile(r))
	}
	var problems []string
	for _, name := range tests {
		n := 0
		for _, re := range res {
			if re.MatchString(name) {
				n++
			}
		}
		switch {
		case n == 0:
			problems = append(problems, pkg+": "+name+" is in no shard slice")
		case n > 1:
			problems = append(problems, pkg+": "+name+" is in "+strconv.Itoa(n)+" shard slices")
		}
	}
	return problems
}

// matrixProblems compares the workflow matrix with the Makefile's
// test-e2e-shard cases and requires the aggregate job.
func matrixProblems(makefile, workflow string) []string {
	m := regexp.MustCompile(`shard:\s*\[([^\]]*)\]`).FindStringSubmatch(workflow)
	if m == nil {
		return []string{"no `shard: [...]` matrix in .github/workflows/test.yml"}
	}
	var matrix []string
	for _, s := range strings.Split(m[1], ",") {
		matrix = append(matrix, strings.TrimSpace(s))
	}
	start := strings.Index(makefile, "test-e2e-shard:")
	if start < 0 {
		return []string{"test-e2e-shard not found in the Makefile"}
	}
	end := strings.Index(makefile[start:], "esac")
	if end < 0 {
		return []string{"test-e2e-shard has no esac in the Makefile"}
	}
	var cases []string
	for _, c := range regexp.MustCompile(`(?m)^\t  ([a-z0-9_]+)\)`).FindAllStringSubmatch(makefile[start:start+end], -1) {
		cases = append(cases, c[1])
	}
	var problems []string
	sort.Strings(matrix)
	sort.Strings(cases)
	if strings.Join(matrix, ",") != strings.Join(cases, ",") {
		problems = append(problems, "the workflow matrix runs shards "+strings.Join(matrix, ",")+" but the Makefile defines "+strings.Join(cases, ","))
	}
	if !strings.Contains(workflow, "name: e2e (all)") {
		problems = append(problems, "the workflow has no aggregate `e2e (all)` job")
	}
	return problems
}

// inShardPath reports whether the go package pkg is named by the shard path p
// (`./tests/e2e/x/...` covers everything below x; a bare directory covers itself).
func inShardPath(pkg, p string) bool {
	if base, ok := strings.CutSuffix(p, "/..."); ok {
		return pkg == base || strings.HasPrefix(pkg, base+"/")
	}
	return pkg == strings.TrimSuffix(p, "/")
}

func readRepoFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
