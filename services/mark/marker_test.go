package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommentLeader(t *testing.T) {
	cases := map[string]string{
		"a.go": "//", "a.ts": "//", "a.tsx": "//", "a.swift": "//",
		"a.py": "#", "a.rb": "#", "a.sh": "#", "a.yaml": "#", "a.yml": "#", "a.md": "#",
		"schema.sql": "--", "migrations/001_init.sql": "--",
	}
	for path, want := range cases {
		got, err := CommentLeader(path)
		if err != nil {
			t.Fatalf("CommentLeader(%q) error: %v", path, err)
		}
		if got != want {
			t.Errorf("CommentLeader(%q)=%q want %q", path, got, want)
		}
	}
	if _, err := CommentLeader("a.txt"); err == nil {
		t.Error("CommentLeader(.txt): want error for unsupported extension, got nil")
	}
}

// TestWriteMarker_KindIsParametrized proves the kind is woven into the emitted comment, so a new
// marker kind is just a different `kind` argument.
func TestWriteMarker_KindIsParametrized(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")
	mustWrite(t, p, "package main\n\nfunc F() {}\n")

	if err := WriteMarker(p, "blueprint", "user.User.email", 3); err != nil {
		t.Fatal(err)
	}
	assertContains(t, p, "// sr:blueprint user.User.email")

	if err := WriteMarker(p, "otherkind", "user.User.role", 1); err != nil {
		t.Fatal(err)
	}
	assertContains(t, p, "// sr:otherkind user.User.role")
}

func TestWriteMarker_Idempotent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")
	mustWrite(t, p, "package main\n\nfunc F() {}\n")

	for i := 0; i < 3; i++ {
		if err := WriteMarker(p, "blueprint", "a.B.c", 3); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(readFile(t, p), "sr:blueprint a.B.c"); n != 1 {
		t.Errorf("idempotency: want exactly 1 marker, got %d", n)
	}
}

func TestWriteMarker_InheritsIndentAndPythonLeader(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "svc.py")
	mustWrite(t, p, "def f():\n    return charge()\n")

	if err := WriteMarker(p, "blueprint", "billing.Svc.charge", 2); err != nil {
		t.Fatal(err)
	}
	// Line 2 is indented 4 spaces; the marker should inherit that indent and use the # leader.
	assertContains(t, p, "    # sr:blueprint billing.Svc.charge")
}

func TestWriteMarker_UnsupportedExtensionFailsLoud(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	mustWrite(t, p, "hello\n")
	if err := WriteMarker(p, "blueprint", "a.B.c", 1); err == nil {
		t.Fatal("want error for unsupported extension, got nil")
	}
}

// TestWriteMarker_MarkdownFrontMatter proves a marker targeting a line inside a markdown file's
// YAML front matter block is written with the "#" leader, same as a .yaml file.
func TestWriteMarker_MarkdownFrontMatter(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "spec.md")
	mustWrite(t, p, "---\n"+ // 1
		"title: user spec\n"+ // 2  ← target
		"---\n\n"+ // 3
		"# User\n\nBody text.\n")

	if err := WriteMarker(p, "blueprint", "user.User.email", 2); err != nil {
		t.Fatal(err)
	}
	assertContains(t, p, "# sr:blueprint user.User.email")
	got := readFile(t, p)
	if strings.Index(got, "# sr:blueprint") > strings.LastIndex(got, "---\n\n") {
		t.Errorf("marker landed outside the front matter block:\n%s", got)
	}
}

// TestWriteMarker_MarkdownOutsideFrontMatterFailsLoud proves a target line in the markdown BODY
// (outside the front matter block) is rejected rather than silently emitting a "#" that markdown
// would render as a heading.
func TestWriteMarker_MarkdownOutsideFrontMatterFailsLoud(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "spec.md")
	mustWrite(t, p, "---\ntitle: x\n---\n\nBody line.\n")

	if err := WriteMarker(p, "blueprint", "a.B.c", 5); err == nil {
		t.Fatal("want error for line outside front matter, got nil")
	}
}

// TestWriteMarker_MarkdownNoFrontMatterFailsLoud proves a markdown file with no front matter
// block at all is rejected rather than guessing where "#" is safe to insert.
func TestWriteMarker_MarkdownNoFrontMatterFailsLoud(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "plain.md")
	mustWrite(t, p, "# Heading\n\nBody.\n")

	if err := WriteMarker(p, "blueprint", "a.B.c", 1); err == nil {
		t.Fatal("want error for markdown file with no front matter, got nil")
	}
}

func TestParsePathLine(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		path, line, err := parsePathLine("src/a/b.go:42")
		if err != nil {
			t.Fatal(err)
		}
		if path != "src/a/b.go" || line != 42 {
			t.Errorf("got (%q,%d) want (src/a/b.go,42)", path, line)
		}
	})
	t.Run("path with colon splits on LAST colon", func(t *testing.T) {
		path, line, err := parsePathLine("C:/win/path.go:7")
		if err != nil {
			t.Fatal(err)
		}
		if path != "C:/win/path.go" || line != 7 {
			t.Errorf("got (%q,%d) want (C:/win/path.go,7)", path, line)
		}
	})
	bad := []string{"noColon", ":12", "x.go:0", "x.go:-1", "x.go:abc"}
	for _, v := range bad {
		if _, _, err := parsePathLine(v); err == nil {
			t.Errorf("parsePathLine(%q): want error, got nil", v)
		}
	}
}

// TestRunKind_MultiPairOneCall proves many `--<fqn>=<path>:<line>` pairs in one invocation all
// land, exercising the dynamic-flag registration + runKind path end-to-end.
func TestRunKind_MultiPairOneCall(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	b := filepath.Join(dir, "b.go")
	mustWrite(t, a, "package main\n\nfunc A() {}\n")
	mustWrite(t, b, "package main\n\nfunc B() {}\n")

	root := newRoot()
	argv := []string{"blueprint",
		"--user.User.email=" + a + ":3",
		"--order.Cart.total=" + b + ":3",
	}
	registerDynamicMarkerFlags(root, argv)
	root.SetArgs(argv)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	assertContains(t, a, "// sr:blueprint user.User.email")
	assertContains(t, b, "// sr:blueprint order.Cart.total")
}

// TestRunKind_MultiMarkerSameFileNoShift is the regression for the sr-mark off-by-one:
// several markers in the SAME file, computed against the ORIGINAL line numbers, must each anchor
// directly above their intended target line. The naive implementation wrote markers in
// fqn-alphabetical order, each WriteMarker re-reading the file — so a lower-line marker written
// first shifted every higher-line target down by one (SQL constraints mis-anchored one field
// early). The fix writes bottom-up (descending line per file). Here the fqn order
// (a_first < m_middle < z_last) is DELIBERATELY the OPPOSITE of line order, so the old code
// would drift every marker; the fix must place each exactly.
func TestRunKind_MultiMarkerSameFileNoShift(t *testing.T) {
	dir := t.TempDir()
	// A .sql migration mirroring the fixture shape: each constraint on its own line.
	p := filepath.Join(dir, "001_init.sql")
	mustWrite(t, p,
		"CREATE TABLE products (\n"+ // 1
			"  id UUID PRIMARY KEY,\n"+ // 2
			"  sku TEXT NOT NULL UNIQUE,\n"+ // 3  ← a_sku_unique targets here
			"  price NUMERIC NOT NULL CHECK (price >= 0),\n"+ // 4  ← m_price_nonneg
			"  reserved INT NOT NULL CHECK (reserved >= 0)\n"+ // 5  ← z_reserved_nonneg
			");\n") // 6

	root := newRoot()
	// fqn-alphabetical order (a<m<z) is the REVERSE of target-line order (3<4<5) → worst case.
	argv := []string{"blueprint",
		"--catalog.a_sku_unique=" + p + ":3",
		"--catalog.m_price_nonneg=" + p + ":4",
		"--catalog.z_reserved_nonneg=" + p + ":5",
	}
	registerDynamicMarkerFlags(root, argv)
	root.SetArgs(argv)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	// Each marker comment must sit on the line IMMEDIATELY above its intended constraint — verify
	// by adjacency, not just presence, so a shifted marker fails.
	lines := strings.Split(readFile(t, p), "\n")
	adjacent := func(markerFQN, targetSubstr string) {
		t.Helper()
		comment := "-- sr:blueprint " + markerFQN
		for i, ln := range lines {
			if strings.TrimSpace(ln) == comment {
				if i+1 < len(lines) && strings.Contains(lines[i+1], targetSubstr) {
					return
				}
				t.Errorf("marker %q is at line %d but the next line is %q, not the intended %q",
					markerFQN, i+1, strings.TrimSpace(lines[min(i+1, len(lines)-1)]), targetSubstr)
				return
			}
		}
		t.Errorf("marker %q not found in file", markerFQN)
	}
	adjacent("catalog.a_sku_unique", "sku TEXT NOT NULL UNIQUE")
	adjacent("catalog.m_price_nonneg", "price NUMERIC NOT NULL CHECK")
	adjacent("catalog.z_reserved_nonneg", "reserved INT NOT NULL CHECK")
}

// TestRunKind_RootRelative proves --root resolves a relative path.
func TestRunKind_RootRelative(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "rel.ts"), "export const x = 1\n")

	root := newRoot()
	argv := []string{"blueprint", "--root", dir, "--m.M.f=rel.ts:1"}
	registerDynamicMarkerFlags(root, argv)
	root.SetArgs(argv)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	assertContains(t, filepath.Join(dir, "rel.ts"), "// sr:blueprint m.M.f")
}

// TestDeleteMarkers_RemovesOnlyNamedFQNs proves deletion targets exactly the given fqns and
// leaves an untargeted marker (and the surrounding code) untouched.
func TestDeleteMarkers_RemovesOnlyNamedFQNs(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "order.go")
	mustWrite(t, p,
		"package order\n\nfunc Cancel() {\n"+
			"\t// sr:blueprint order.cancel_only_pending\n"+
			"\tif status != \"pending\" {\n\t\treturn\n\t}\n"+
			"\t// sr:blueprint order.total_nonneg\n"+
			"\t_ = total\n}\n")

	n, err := DeleteMarkers(dir, "blueprint", []string{"order.cancel_only_pending"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("removed=%d want 1", n)
	}
	got := readFile(t, p)
	if strings.Contains(got, "order.cancel_only_pending") {
		t.Error("targeted marker still present")
	}
	if !strings.Contains(got, "// sr:blueprint order.total_nonneg") {
		t.Error("untargeted marker was removed")
	}
	if !strings.Contains(got, `if status != "pending"`) {
		t.Error("surrounding code was removed")
	}
}

// TestDeleteMarkers_NoMatchIsNoop proves a delete for an fqn with no marker anywhere is a
// silent no-op (0 removed, file untouched), not an error.
func TestDeleteMarkers_NoMatchIsNoop(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")
	content := "package x\n\n// sr:blueprint x.kept\nfunc F() {}\n"
	mustWrite(t, p, content)

	n, err := DeleteMarkers(dir, "blueprint", []string{"x.nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("removed=%d want 0", n)
	}
	if readFile(t, p) != content {
		t.Error("file was modified despite no matching marker")
	}
}

// TestDeleteMarkers_LeavesInlineMarkerLineAlone proves a marker sharing its line with real code
// (not how sr-mark itself writes markers, but defensive against hand-authored files) is left
// alone rather than risk deleting logic — the line must be SOLELY the marker comment to qualify.
func TestDeleteMarkers_LeavesInlineMarkerLineAlone(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")
	content := "package x\n\nfoo() // sr:blueprint x.inline\n"
	mustWrite(t, p, content)

	n, err := DeleteMarkers(dir, "blueprint", []string{"x.inline"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("removed=%d want 0 (inline marker line must be left alone)", n)
	}
	if readFile(t, p) != content {
		t.Error("file was modified for an inline (non-own-line) marker")
	}
}

// TestDeleteMarkers_RespectsGitignore proves a marker sitting in a file excluded by .gitignore is
// left untouched — the scanner has no business rewriting a file the user has told git to ignore,
// same reasoning as skipping node_modules/.git.
func TestDeleteMarkers_RespectsGitignore(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	mustWrite(t, filepath.Join(dir, ".gitignore"), "ignored.go\n")
	ignored := filepath.Join(dir, "ignored.go")
	tracked := filepath.Join(dir, "tracked.go")
	mustWrite(t, ignored, "package x\n\n// sr:blueprint x.rule\nfunc F() {}\n")
	mustWrite(t, tracked, "package x\n\n// sr:blueprint x.rule\nfunc G() {}\n")

	n, err := DeleteMarkers(dir, "blueprint", []string{"x.rule"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("removed=%d want 1 (only the tracked, non-ignored file)", n)
	}
	if !strings.Contains(readFile(t, ignored), "sr:blueprint x.rule") {
		t.Error("gitignored file's marker was removed — DeleteMarkers must skip gitignored files")
	}
	if strings.Contains(readFile(t, tracked), "sr:blueprint x.rule") {
		t.Error("tracked file's marker should have been removed")
	}
}

// --- helpers ---

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertContains(t *testing.T, path, want string) {
	t.Helper()
	got := readFile(t, path)
	if !strings.Contains(got, want) {
		t.Errorf("file %s does not contain %q\n--- content ---\n%s", path, want, got)
	}
}
