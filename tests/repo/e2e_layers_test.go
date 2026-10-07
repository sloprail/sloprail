package repo

import (
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// tests/e2e has two layers, and a package's layer is a fact about its code, not a
// choice: a package under tests/e2e/harness/<area>/ starts the mock agent (one of
// its functions reaches harness.Env.drive, the one place the mock is started), and a
// package under tests/e2e/cli/<area>/ never does. Callees are resolved by the type
// checker, through package-local helpers and harness methods alike, so the layer
// cannot drift from what the tests do. (tests/e2e/harness/*.go is the shared helper
// package itself, in neither layer.)

func TestE2EPackagesSitInTheirLayer(t *testing.T) {
	for _, p := range layerProblems(t, repoRoot(t)) {
		t.Error(p)
	}
}

// --- negative test: the guard fires ---

func TestGuardFiresOnAPackageInTheWrongLayer(t *testing.T) {
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
	write("go.mod", "module example.com/m\n\ngo 1.25.0\n")
	write("tests/e2e/harness/harness.go", "package harness\n\ntype Env struct{}\n\nfunc (e *Env) drive() {}\nfunc (e *Env) Run()   { e.drive() }\nfunc (e *Env) Build() {}\n")
	pkg := func(dir, call string) {
		write(dir+"/main_test.go", "package x\n\nimport \"example.com/m/tests/e2e/harness\"\n\nfunc helper(e *harness.Env) { e."+call+"() }\n")
		write(dir+"/a_test.go", "package x\n\nimport (\n\t\"testing\"\n\n\t\"example.com/m/tests/e2e/harness\"\n)\n\nfunc TestA(t *testing.T) { helper(&harness.Env{}) }\n")
	}
	pkg("tests/e2e/cli/area/001_builds", "Build")
	pkg("tests/e2e/harness/area/002_drives", "Run")
	pkg("tests/e2e/cli/area/003_drives", "Run")
	pkg("tests/e2e/harness/area/004_builds", "Build")

	got := strings.Join(layerProblems(t, root), "\n")
	for _, bad := range []string{"cli/area/003_drives", "harness/area/004_builds"} {
		if !strings.Contains(got, bad) {
			t.Errorf("%s, in the wrong layer, was not reported:\n%s", bad, got)
		}
	}
	for _, good := range []string{"001_builds", "002_drives"} {
		if strings.Contains(got, good) {
			t.Errorf("%s, in its layer, was reported:\n%s", good, got)
		}
	}
}

// --- the check ---

// layerProblems reports every test package under root/tests/e2e/{cli,harness}/ whose
// code disagrees with its layer.
func layerProblems(t *testing.T, root string) []string {
	t.Helper()
	cfg := &packages.Config{
		Dir:   root,
		Tests: true,
		Mode:  packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Env:   append(os.Environ(), "GOWORK=off"),
	}
	pkgs, err := packages.Load(cfg, "./tests/e2e/...")
	if err != nil {
		t.Fatalf("load ./tests/e2e/...: %v", err)
	}
	var loadErrs []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			loadErrs = append(loadErrs, e.Error())
		}
	})
	if len(loadErrs) > 0 {
		t.Fatalf("./tests/e2e/... does not type-check:\n%s", strings.Join(loadErrs, "\n"))
	}

	// calls: every function's callees, by full name (a package and its test variant
	// hold distinct objects for one function). dirOf: the directory declaring it.
	calls := map[string]map[string]bool{}
	dirOf := map[string]string{}
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if p.TypesInfo == nil || !strings.Contains(p.PkgPath, "/tests/e2e") {
			return
		}
		for _, f := range p.Syntax {
			dir, _ := filepath.Rel(root, filepath.Dir(p.Fset.File(f.Pos()).Name()))
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				obj, _ := p.TypesInfo.Defs[fd.Name].(*types.Func)
				if obj == nil {
					continue
				}
				name := filepath.ToSlash(dir) + " " + obj.FullName()
				m := calls[name]
				if m == nil {
					m = map[string]bool{}
					calls[name] = m
				}
				dirOf[name] = filepath.ToSlash(dir)
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					if id, ok := n.(*ast.Ident); ok {
						if c, ok := p.TypesInfo.Uses[id].(*types.Func); ok {
							m[c.Origin().FullName()] = true
						}
					}
					return true
				})
			}
		}
	})

	// drives: the full names that reach harness.Env.drive, closed transitively.
	drives := map[string]bool{}
	for name := range calls {
		if full := name[strings.Index(name, " ")+1:]; strings.HasSuffix(full, "tests/e2e/harness.Env).drive") {
			drives[full] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for name, cs := range calls {
			full := name[strings.Index(name, " ")+1:]
			if drives[full] {
				continue
			}
			for c := range cs {
				if drives[c] {
					drives[full] = true
					changed = true
					break
				}
			}
		}
	}

	reaches := map[string]bool{}
	for name, dir := range dirOf {
		if _, ok := reaches[dir]; !ok {
			reaches[dir] = false
		}
		if drives[name[strings.Index(name, " ")+1:]] {
			reaches[dir] = true
		}
	}
	var problems []string
	for dir, r := range reaches {
		switch {
		case strings.HasPrefix(dir, "tests/e2e/cli/") && r:
			problems = append(problems, "./"+dir+" starts the mock agent (it reaches harness.Env.drive): it belongs under tests/e2e/harness/")
		case strings.HasPrefix(dir, "tests/e2e/harness/") && !r && hasTests(root, dir):
			problems = append(problems, "./"+dir+" never starts the mock agent (nothing in it reaches harness.Env.drive): it belongs under tests/e2e/cli/")
		}
	}
	sort.Strings(problems)
	return problems
}

func hasTests(root, dir string) bool {
	m, _ := filepath.Glob(filepath.Join(root, dir, "*_test.go"))
	return len(m) > 0
}
