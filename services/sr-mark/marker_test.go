package main

import (
	"io"
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

// sr:proves cli/mark-apply-is-idempotent
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

// TestApply_MultiPairOneCall proves many `--<fqn>=<path>:<line>` pairs in one invocation all
// land, exercising the apply argument parser + runApply path end-to-end.
func TestApply_MultiPairOneCall(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	b := filepath.Join(dir, "b.go")
	mustWrite(t, a, "package main\n\nfunc A() {}\n")
	mustWrite(t, b, "package main\n\nfunc B() {}\n")

	runRoot(t, "apply", "blueprint",
		"--user.User.email="+a+":3",
		"--order.Cart.total="+b+":3",
	)
	assertContains(t, a, "// sr:blueprint user.User.email")
	assertContains(t, b, "// sr:blueprint order.Cart.total")
}

// TestApply_KindIsFreeformData is the point of the whole restructure: the kind is an ARGUMENT, so
// a kind nobody wired into the code works exactly like a built-in one. `docs` has no subcommand,
// no registration, no mention in main.go — it is just a word the caller chose.
func TestApply_KindIsFreeformData(t *testing.T) {
	dir := t.TempDir()
	for _, kind := range []string{"docs", "blueprint", "blueprint:test", "some-new_kind.v2"} {
		p := filepath.Join(dir, "x.go")
		mustWrite(t, p, "package main\n\nfunc F() {}\n")
		runRoot(t, "apply", kind, "--x.y="+p+":3")
		assertContains(t, p, "// sr:"+kind+" x.y")
	}
}

// TestApply_RejectsInvalidKind proves the kind rule is enforced at the boundary: alphanumeric
// (plus the `:`/`_`/`-`/`.` separators a kind namespaces itself with), nothing else. A kind
// carrying a space or a shell/comment metacharacter would corrupt the written `sr:<kind> <fqn>`
// line, so it never reaches a file.
// sr:proves cli/mark-apply-invalid-writes-nothing
func TestApply_RejectsInvalidKind(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")
	original := "package main\n\nfunc F() {}\n"

	for _, kind := range []string{"bad kind", "bad/kind", "kind!", "kind\tx", "kind\n"} {
		mustWrite(t, p, original)
		if err := runRootErr("apply", kind, "--x.y="+p+":3"); err == nil {
			t.Errorf("apply kind %q: want error, got nil", kind)
		}
		if readFile(t, p) != original {
			t.Errorf("apply kind %q: file was modified despite invalid kind", kind)
		}
	}

	// The valid ones must still pass, so the rule is not simply rejecting everything.
	for _, kind := range []string{"docs", "blueprint", "blueprint:test", "a-b_c.d", "v2"} {
		if err := validateKind(kind); err != nil {
			t.Errorf("validateKind(%q): want nil, got %v", kind, err)
		}
	}
}

// TestApply_RejectsWhitespaceFQN is the silent-corruption regression. An fqn may use anything a
// URL admits, but NOT whitespace: the written form is `sr:<kind> <fqn>` on one line and the
// reader in marker.go is `(\S+)`, so `--a b=...` would write intact and read back truncated to
// "a". Rejecting it at write time is the whole point — a truncated marker fails silently later.
// sr:proves cli/mark-apply-invalid-writes-nothing
func TestApply_RejectsWhitespaceFQN(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")
	original := "package main\n\nfunc F() {}\n"

	for _, fqn := range []string{"a b", "a\tb", "trailing ", " leading", "a\nb"} {
		mustWrite(t, p, original)
		if err := runRootErr("apply", "blueprint", "--"+fqn+"="+p+":3"); err == nil {
			t.Errorf("apply fqn %q: want error, got nil", fqn)
		}
		if readFile(t, p) != original {
			t.Errorf("apply fqn %q: file was modified despite whitespace fqn", fqn)
		}
	}

	// URL-admissible fqns must pass — the rule rejects whitespace, not punctuation.
	for _, fqn := range []string{
		"user.User.email", "order/cart/total", "https://ex.com/a?b=c#d",
		"a-b_c~d", "ns:name", "a%20b", "list[0]", "a+b,c;d", "x!$&'()*=",
	} {
		if err := validateFQN(fqn); err != nil {
			t.Errorf("validateFQN(%q): want nil, got %v", fqn, err)
		}
	}
	// And a genuinely non-URL character is still refused.
	for _, fqn := range []string{"a\"b", "a<b", "a\\b", "a{b}", "a|b", "a^b"} {
		if err := validateFQN(fqn); err == nil {
			t.Errorf("validateFQN(%q): want error, got nil", fqn)
		}
	}
}

// TestApply_WhitespaceFQNIsUnreadable demonstrates WHY the whitespace rule exists, against the
// ACTUAL reader rather than against an assertion about it. The marker regex in marker.go is
// `sr:<kind>\s+(\S+)\s*$` — anchored at the end — so a written `sr:blueprint a b` matches
// NOTHING: the marker is not truncated to "a", it becomes entirely invisible to every reader.
// A marker that writes successfully and can never be found again is the corruption the boundary
// check prevents, so the fix is to refuse it at write time.
func TestApply_WhitespaceFQNIsUnreadable(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")
	mustWrite(t, p, "package main\n\nfunc F() {}\n")

	// Bypass the CLI boundary to write what validation would have refused.
	if err := WriteMarker(p, "blueprint", "a b", 3); err != nil {
		t.Fatal(err)
	}
	assertContains(t, p, "// sr:blueprint a b") // it DID write

	// ...but no fqn finds it: not the full name, not the prefix before the space.
	for _, probe := range []string{"a b", "a", "b"} {
		n, err := DeleteMarkers(dir, "blueprint", []string{probe})
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("delete %q removed=%d want 0 — the space-bearing marker is unreadable by design of the regex", probe, n)
		}
	}
	assertContains(t, p, "// sr:blueprint a b") // still there, unreachable forever

	// The same fqn without the space is readable, proving the space is what breaks it.
	if err := WriteMarker(p, "blueprint", "a.b", 3); err != nil {
		t.Fatal(err)
	}
	if n, _ := DeleteMarkers(dir, "blueprint", []string{"a.b"}); n != 1 {
		t.Errorf("whitespace-free fqn removed=%d want 1", n)
	}
}

// TestApply_FQNContainingEquals is the regression for a bug found by running the real binary,
// not by unit-testing validateFQN in isolation: `--https://ex.com/a?b=c#d=file.go:3` was split on
// the FIRST `=`, which lands inside the fqn's own query string, so the fqn was silently mangled
// into "https://ex.com/a?b" and the leftover "c#d=file.go:3" was treated as the path. validateFQN
// accepted the URL happily — the fault was in the argument split, one layer up. An fqn may
// contain `=` (a URL query is a name the spec explicitly invites); the VALUE `<path>:<line>`
// never can, so the split must take the LAST `=`.
func TestApply_FQNContainingEquals(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.go")
	mustWrite(t, p, "package f\n\nfunc F() {}\n")

	fqn := "https://ex.com/a?b=c#d"
	runRoot(t, "apply", "docs", "--"+fqn+"="+p+":3")
	assertContains(t, p, "// sr:docs "+fqn)

	// And it round-trips: the marker is findable by the exact fqn that wrote it.
	n, err := DeleteMarkers(dir, "docs", []string{fqn})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("removed=%d want 1 — the URL fqn must round-trip", n)
	}
}

// TestDelete_ValidatesKindAndFQN proves delete enforces the same boundary rules as apply — the
// two verbs share one vocabulary, so an fqn that could never have been written must not be
// accepted for deletion either.
func TestDelete_ValidatesKindAndFQN(t *testing.T) {
	dir := t.TempDir()
	if err := runRootErr("delete", "bad kind", "x.y", "--root", dir); err == nil {
		t.Error("delete with invalid kind: want error, got nil")
	}
	if err := runRootErr("delete", "blueprint", "a b", "--root", dir); err == nil {
		t.Error("delete with whitespace fqn: want error, got nil")
	}
}

// TestDelete_RejectsUnknownFlag is the regression for the bug the old dynamic-flag pre-scan
// caused: because it mapped EVERY child of root as a marker kind, `sr-mark delete blueprint
// --foo=bar a.fqn b.fqn` silently registered `--foo` as a string flag on delete and exited 0.
// With the pre-scan gone, delete has only its real flags and an unknown one is an error.
func TestDelete_RejectsUnknownFlag(t *testing.T) {
	dir := t.TempDir()
	if err := runRootErr("delete", "blueprint", "--foo=bar", "a.fqn", "b.fqn", "--root", dir); err == nil {
		t.Error("delete with unknown flag --foo: want error, got nil")
	}
}

// TestApply_RejectsUnknownFlagAndStrayArgs proves apply's own parser refuses what it cannot make
// sense of, rather than ignoring it. A bare `--fqn` with no `=value` is the easy mistake, and a
// second positional argument means the caller thought kind took more than one word.
func TestApply_RejectsUnknownFlagAndStrayArgs(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")
	mustWrite(t, p, "package main\n\nfunc F() {}\n")

	cases := [][]string{
		{"apply", "blueprint", "--user.email"},                           // pair with no =value
		{"apply", "blueprint", "extra", "--x.y=" + p + ":3"},             // stray positional
		{"apply", "blueprint", "-x", "--x.y=" + p + ":3"},                // unknown short flag
		{"apply", "blueprint"},                                           // no pairs at all
		{"apply", "blueprint", "--x.y=" + p + ":3", "--x.y=" + p + ":1"}, // duplicate fqn
	}
	for _, argv := range cases {
		if err := runRootErr(argv...); err == nil {
			t.Errorf("%v: want error, got nil", argv)
		}
	}
}

// TestApply_MultiMarkerSameFileNoShift is the regression for the sr-mark off-by-one:
// several markers in the SAME file, computed against the ORIGINAL line numbers, must each anchor
// directly above their intended target line. The naive implementation wrote markers in
// fqn-alphabetical order, each WriteMarker re-reading the file — so a lower-line marker written
// first shifted every higher-line target down by one (SQL constraints mis-anchored one field
// early). The fix writes bottom-up (descending line per file). Here the fqn order
// (a_first < m_middle < z_last) is DELIBERATELY the OPPOSITE of line order, so the old code
// would drift every marker; the fix must place each exactly.
func TestApply_MultiMarkerSameFileNoShift(t *testing.T) {
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

	// fqn-alphabetical order (a<m<z) is the REVERSE of target-line order (3<4<5) → worst case.
	runRoot(t, "apply", "blueprint",
		"--catalog.a_sku_unique="+p+":3",
		"--catalog.m_price_nonneg="+p+":4",
		"--catalog.z_reserved_nonneg="+p+":5",
	)

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

// TestApply_RootRelative proves --root resolves a relative path. Both spellings are covered
// because apply parses --root in its own tail parser (it must, since flag parsing is disabled to
// admit arbitrary --<fqn> names), so `--root dir` and `--root=dir` are two distinct code paths.
func TestApply_RootRelative(t *testing.T) {
	for _, rootFlag := range []string{"separate", "equals"} {
		t.Run(rootFlag, func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, "rel.ts"), "export const x = 1\n")

			if rootFlag == "separate" {
				runRoot(t, "apply", "blueprint", "--root", dir, "--m.M.f=rel.ts:1")
			} else {
				runRoot(t, "apply", "blueprint", "--root="+dir, "--m.M.f=rel.ts:1")
			}
			assertContains(t, filepath.Join(dir, "rel.ts"), "// sr:blueprint m.M.f")
		})
	}
}

// TestDelete_RootFlag proves delete resolves its tree from --root (cobra's persistent flag, since
// delete parses flags normally).
func TestDelete_RootFlag(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")
	mustWrite(t, p, "package x\n\n// sr:blueprint x.rule\nfunc F() {}\n")

	runRoot(t, "delete", "blueprint", "x.rule", "--root", dir)
	if strings.Contains(readFile(t, p), "sr:blueprint x.rule") {
		t.Error("marker should have been removed via --root")
	}
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

// runRootErr executes a full `sr-mark` argv against a fresh root command and returns the error.
// Fresh per call because cobra commands carry parsed state.
func runRootErr(argv ...string) error {
	root := newRoot()
	root.SetArgs(argv)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	return root.Execute()
}

// runRoot executes an argv and fails the test if it errors.
func runRoot(t *testing.T, argv ...string) {
	t.Helper()
	if err := runRootErr(argv...); err != nil {
		t.Fatalf("sr-mark %v: %v", argv, err)
	}
}

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
