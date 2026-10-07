package repo

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// A harness (Claude Code today, Codex or Cursor later) is selected by one place only:
// the service main packages. Everything else talks to internal/harness, the
// harness-neutral interface, and gets the implementation through it. A package that
// imports internal/harness/<name> directly has hard-wired one harness into code that
// is meant to serve all of them, and the next harness would have to find and unpick
// that import.
//
// So a Go file may import an implementation package, internal/harness/<name>[/...],
// only if it is
//
//   - inside that same implementation (internal/harness/<name>/...),
//   - in a service (services/**), whose main chooses the harness,
//   - a _test.go file (a package's tests choose the harness the way a main does), or
//   - under tests/ (the e2e suites drive one real harness by design).
//
// The check is a function over a tree so the negative test below proves it fires on a
// fake one.

const harnessImportBase = "github.com/sloprail/sloprail/internal/harness/"

func TestHarnessImplementationsAreImportedOnlyByServices(t *testing.T) {
	root := repoRoot(t)
	problems := harnessImportProblems(t, root)
	if len(problems) > 0 {
		t.Fatalf("a package outside internal/harness/<name>/ imports a harness implementation "+
			"(consume internal/harness instead; only services/* mains choose the harness):\n  %s",
			strings.Join(problems, "\n  "))
	}
}

func TestHarnessImportGuardFiresOnAFakeTree(t *testing.T) {
	root := t.TempDir()
	write := func(rel, src string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	imp := func(pkg, path string) string {
		return "package " + pkg + "\n\nimport _ \"" + path + "\"\n"
	}
	cc := harnessImportBase + "claudecode"

	// Allowed: the implementation itself, a service, a _test.go file, the e2e tree,
	// and the neutral package imported by anyone.
	write("internal/harness/claudecode/a.go", imp("claudecode", harnessImportBase+"claudecode/sub"))
	write("services/sr-x/main.go", imp("main", cc))
	write("internal/engine/x_test.go", imp("engine", cc))
	write("tests/e2e/y/y.go", imp("y", cc))
	write("internal/engine/neutral.go", imp("engine", "github.com/sloprail/sloprail/internal/harness"))
	if got := harnessImportProblemsIn(t, root); len(got) != 0 {
		t.Fatalf("allowed imports were flagged: %v", got)
	}

	// Refused: generic code, and one implementation importing another.
	write("internal/engine/leak.go", imp("engine", cc))
	write("internal/harness/codex/a.go", imp("codex", cc))
	got := harnessImportProblemsIn(t, root)
	want := []string{"internal/engine/leak.go", "internal/harness/codex/a.go"}
	if len(got) != len(want) {
		t.Fatalf("flagged %v, want files %v", got, want)
	}
	for i, w := range want {
		if !strings.HasPrefix(got[i], w+" ") {
			t.Errorf("problem %d = %q, want it to name %s", i, got[i], w)
		}
	}
}

func harnessImportProblems(t *testing.T, root string) []string {
	t.Helper()
	problems := harnessImportProblemsIn(t, root)
	// The real tree must actually have an implementation, or the guard is vacuous.
	if _, err := os.Stat(filepath.Join(root, "internal", "harness", "claudecode")); err != nil {
		t.Fatalf("internal/harness/claudecode is gone; the guard would pass vacuously: %v", err)
	}
	return problems
}

// harnessImportProblemsIn lists, sorted, every "<file> imports <pkg>" that breaks the
// rule above in the Go files under root.
func harnessImportProblemsIn(t *testing.T, root string) []string {
	t.Helper()
	var problems []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == ".claude" || name == "node_modules" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		relPath := filepath.ToSlash(rel(root, path))
		f, perr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if perr != nil {
			return nil // not this guard's business; the compiler reports it
		}
		for _, im := range f.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			if !strings.HasPrefix(p, harnessImportBase) {
				continue
			}
			impl := strings.SplitN(strings.TrimPrefix(p, harnessImportBase), "/", 2)[0]
			if harnessImportAllowed(relPath, impl) {
				continue
			}
			problems = append(problems, relPath+" imports "+p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(problems)
	return problems
}

// harnessImportAllowed says whether the file at relPath (slash-separated, from the repo
// root) may import the implementation named impl.
func harnessImportAllowed(relPath, impl string) bool {
	switch {
	case strings.HasPrefix(relPath, "internal/harness/"+impl+"/"):
		return true
	case strings.HasPrefix(relPath, "services/"):
		return true
	case strings.HasPrefix(relPath, "tests/"):
		return true
	case strings.HasSuffix(relPath, "_test.go"):
		return true
	}
	return false
}
