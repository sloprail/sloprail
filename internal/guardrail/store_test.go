package guardrail

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitFrontmatter_HappyPath(t *testing.T) {
	front, body, err := splitFrontmatter([]byte("---\nenabled: true\n---\nThe rule.\n"))
	require.NoError(t, err)
	assert.Equal(t, "enabled: true\n", string(front))
	assert.Equal(t, "The rule.\n", string(body))
}

func TestSplitFrontmatter_NoOpeningFence(t *testing.T) {
	for name, in := range map[string]string{
		"empty input":  "",
		"prose only":   "Just prose, no fence.\n",
		"fence second": "title\n---\nenabled: true\n---\nbody\n",
	} {
		t.Run(name, func(t *testing.T) {
			front, body, err := splitFrontmatter([]byte(in))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "no frontmatter")
			assert.Nil(t, front)
			assert.Nil(t, body)
		})
	}
}

func TestSplitFrontmatter_Unterminated(t *testing.T) {
	for name, in := range map[string]string{
		"fence with no newline": "---",
		"fence then eof":        "---\n",
		"fence then yaml":       "---\nenabled: true\n",
		"fence then prose":      "---\nenabled: true\nbody without a closing fence\n",
	} {
		t.Run(name, func(t *testing.T) {
			front, body, err := splitFrontmatter([]byte(in))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unterminated frontmatter")
			assert.Nil(t, front)
			assert.Nil(t, body)
		})
	}
}

func TestSplitFrontmatter_EmptyBody(t *testing.T) {
	// A declaration with frontmatter and nothing beneath it is well-formed;
	// it simply has no rubric for a judge to read.
	front, body, err := splitFrontmatter([]byte("---\nenabled: true\n---\n"))
	require.NoError(t, err)
	assert.Equal(t, "enabled: true\n", string(front))
	assert.Empty(t, string(body))
}

func TestSplitFrontmatter_EmptyFrontmatter(t *testing.T) {
	front, body, err := splitFrontmatter([]byte("---\n---\nThe rule.\n"))
	require.NoError(t, err)
	assert.Empty(t, string(front))
	assert.Equal(t, "The rule.\n", string(body))
}

func TestSplitFrontmatter_BothEmpty(t *testing.T) {
	front, body, err := splitFrontmatter([]byte("---\n---"))
	require.NoError(t, err)
	assert.Empty(t, string(front))
	assert.Empty(t, string(body))
}

// TestSplitFrontmatter_BodySurvivesByteForByte is the load-bearing one. A judge
// hook reads the body as its rubric, so any normalisation — trimming, newline
// folding, unicode canonicalisation — would change what is being judged
// against without anyone editing the rule.
func TestSplitFrontmatter_BodySurvivesByteForByte(t *testing.T) {
	bodies := map[string]string{
		"trailing spaces":     "The rule.   \nAnd more.  \n",
		"trailing tabs":       "The rule.\t\t\n",
		"no trailing newline": "The rule with no final newline",
		"leading blank lines": "\n\n\nThe rule.\n",
		"interior blanks":     "One.\n\n\n\nTwo.\n",
		"trailing blanks":     "The rule.\n\n\n\n",
		"crlf line endings":   "The rule.\r\nAnd more.\r\n",
		"mixed endings":       "One.\r\nTwo.\nThree.\r\n",
		"unicode":             "Правило: не сломай. 規則。\nÉmoji: ✅ 🚧 — ok.\n",
		"combining marks":     "é vs é must stay distinct\n",
		"zero width":          "in​visible‍ joiner\n",
		"nbsp":                "hard space stays hard\n",
		"tabs indentation":    "\tindented\n\t\tdeeper\n",
		"markdown fences":     "Run this:\n\n```sh\necho hi\n```\n",
		"three dashes inside": "See below.\n---\nA horizontal rule, not a fence.\n",
		"yaml-looking body":   "enabled: false\nhooks:\n  - a\n",
		"only whitespace":     "   \n\t\n  ",
		"null byte":           "before\x00after\n",
		"very long line":      string(make([]byte, 0, 4096)) + longLine(4096),
	}

	for name, want := range bodies {
		t.Run(name, func(t *testing.T) {
			doc := "---\nenabled: true\n---\n" + want
			front, body, err := splitFrontmatter([]byte(doc))
			require.NoError(t, err)
			assert.Equal(t, "enabled: true\n", string(front))
			require.Equal(t, want, string(body), "body must survive byte for byte")
			require.Equal(t, len(want), len(body), "byte length must be identical")
		})
	}
}

func longLine(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return string(b)
}

func TestSplitFrontmatter_FirstClosingFenceWins(t *testing.T) {
	// A later --- is body, not a second frontmatter block.
	_, body, err := splitFrontmatter([]byte("---\na: 1\n---\nbody\n---\nmore\n"))
	require.NoError(t, err)
	assert.Equal(t, "body\n---\nmore\n", string(body))
}

// TestSplitFrontmatter_FenceMatchingIsLoose DOCUMENTS A KNOWN DEFECT.
//
// This test asserts what the code does today, NOT what it should do. When the
// defect is fixed, this test MUST be changed — its failure is the expected
// consequence of the fix, not a regression. Do not "repair" it by reverting
// the fix.
//
// The defect: the opening fence is matched with HasPrefix after TrimSpace
// (store.go:100) while the closing fence is matched with Equal after TrimSpace
// (store.go:105). So `----` and `---yaml` open frontmatter but only `---`
// closes it, and the asymmetry is documented nowhere.
//
// Corrected behaviour would be: both fences matched the same way, exactly
// `---` after trimming, so that `----`, `---yaml` and any other prefix match
// is refused as an opening fence with the "no frontmatter" error. The
// subtests below marked DEFECT are the ones whose expectation would flip from
// NoError to Error; "four dashes rejected as closing fence" already asserts
// the corrected behaviour and would keep passing.
func TestSplitFrontmatter_FenceMatchingIsLoose(t *testing.T) {
	t.Run("indented opening fence accepted", func(t *testing.T) {
		// Whether indentation should be tolerated is a judgement call for
		// whoever fixes the asymmetry; either way both fences should agree.
		front, body, err := splitFrontmatter([]byte("   ---\na: 1\n---\nbody\n"))
		require.NoError(t, err)
		assert.Equal(t, "a: 1\n", string(front))
		assert.Equal(t, "body\n", string(body))
	})

	t.Run("four dashes accepted as opening fence", func(t *testing.T) {
		// DEFECT: should be refused. Flip to require.Error once fixed.
		front, _, err := splitFrontmatter([]byte("----\na: 1\n---\nbody\n"))
		require.NoError(t, err)
		assert.Equal(t, "a: 1\n", string(front))
	})

	t.Run("opening fence with trailing text accepted", func(t *testing.T) {
		// DEFECT: should be refused. Flip to require.Error once fixed.
		front, _, err := splitFrontmatter([]byte("---yaml\na: 1\n---\nbody\n"))
		require.NoError(t, err)
		assert.Equal(t, "a: 1\n", string(front))
	})

	t.Run("four dashes rejected as closing fence", func(t *testing.T) {
		// Already the corrected behaviour: this subtest survives the fix.
		_, _, err := splitFrontmatter([]byte("---\na: 1\n----\nbody\n"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unterminated")
	})

	t.Run("indented closing fence accepted", func(t *testing.T) {
		// TrimSpace runs before the Equal, so indentation is tolerated here.
		_, body, err := splitFrontmatter([]byte("---\na: 1\n   ---   \nbody\n"))
		require.NoError(t, err)
		assert.Equal(t, "body\n", string(body))
	})
}

// --- Load -------------------------------------------------------------------

// writeGuardrail lays one declaration down under a store root.
func writeGuardrail(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, "guardrails", name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "GUARDRAIL.md"), []byte(content), 0o644))
}

func TestLoad_NoDotDirectory(t *testing.T) {
	// A project that has not adopted sloprail is not a broken project.
	s := New(filepath.Join(t.TempDir(), "nothing-here"))
	decls, invalid, err := s.Load()
	require.NoError(t, err)
	assert.Empty(t, decls)
	assert.Empty(t, invalid)
}

func TestLoad_DotDirectoryWithoutGuardrailsDir(t *testing.T) {
	root := t.TempDir() // exists, but has no guardrails/ inside
	decls, invalid, err := New(root).Load()
	require.NoError(t, err)
	assert.Empty(t, decls)
	assert.Empty(t, invalid)
}

func TestLoad_EmptyGuardrailsDir(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "guardrails"), 0o755))

	decls, invalid, err := New(root).Load()
	require.NoError(t, err)
	assert.Empty(t, decls)
	assert.Empty(t, invalid)
}

// TestLoad_UnreadableDirectoryIsAnError separates "no guardrails" from
// "cannot tell whether there are guardrails". A missing directory is the
// ordinary state of a project that has not adopted sloprail; a directory that
// exists but cannot be read is a broken setup, and reporting it as no rules
// would silently disarm the project.
func TestLoad_UnreadableDirectoryIsAnError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits do not apply on windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root ignores the mode, so the directory stays readable")
	}

	root := t.TempDir()
	dir := filepath.Join(root, "guardrails")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	writeGuardrail(t, root, "unreachable", "---\nhooks: {}\n---\nbody\n")

	require.NoError(t, os.Chmod(dir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) }) // so TempDir can clean up

	decls, invalid, err := New(root).Load()
	require.Error(t, err, "an unreadable directory is not an empty one")
	assert.Empty(t, decls)
	assert.Empty(t, invalid)
	assert.Contains(t, err.Error(), "guardrail: read")
	assert.Contains(t, err.Error(), dir, "the error names the directory it could not read")
}

func TestLoad_HappyPath(t *testing.T) {
	root := t.TempDir()
	writeGuardrail(t, root, "no-slop", `---
enabled: true
hooks:
  PreFileCreate:
    - matcher: path startsWith "memories/"
      hooks:
        - type: command
          command: ./judge.sh
---
Memories must cite their source.
`)

	decls, invalid, err := New(root).Load()
	require.NoError(t, err)
	require.Empty(t, invalid)
	require.Len(t, decls, 1)

	d := decls[0]
	assert.Equal(t, "no-slop", d.Name, "the name comes from the folder, not the frontmatter")
	assert.True(t, d.IsEnabled())
	assert.Equal(t, filepath.Join(root, "guardrails", "no-slop"), d.Dir)
	assert.Equal(t, "Memories must cite their source.\n", d.Body)

	require.Len(t, d.Hooks["PreFileCreate"], 1)
	b := d.Hooks["PreFileCreate"][0]
	assert.Equal(t, `path startsWith "memories/"`, b.Matcher)
	require.Len(t, b.Hooks, 1)
	assert.Equal(t, HookCommand, b.Hooks[0].Type)
	assert.Equal(t, "./judge.sh", b.Hooks[0].Command)
}

func TestLoad_BodyReachesDeclarationByteForByte(t *testing.T) {
	root := t.TempDir()
	body := "Rubric:  \n\n  1. Cite the source.\t\n\nЮникод ✅\n\n\n"
	writeGuardrail(t, root, "rubric", "---\nenabled: true\n---\n"+body)

	decls, invalid, err := New(root).Load()
	require.NoError(t, err)
	require.Empty(t, invalid)
	require.Len(t, decls, 1)
	assert.Equal(t, body, decls[0].Body, "what the judge reads must be what was written")
}

func TestLoad_EnabledDefaultsToTrue(t *testing.T) {
	root := t.TempDir()
	writeGuardrail(t, root, "unstated", "---\nhooks: {}\n---\nbody\n")
	writeGuardrail(t, root, "off", "---\nenabled: false\nhooks: {}\n---\nbody\n")

	decls, invalid, err := New(root).Load()
	require.NoError(t, err)
	require.Empty(t, invalid)
	require.Len(t, decls, 2)

	byName := map[string]Declaration{}
	for _, d := range decls {
		byName[d.Name] = d
	}
	assert.True(t, byName["unstated"].IsEnabled(), "saying nothing about being off means on")
	assert.Nil(t, byName["unstated"].Enabled)
	assert.False(t, byName["off"].IsEnabled())
	require.NotNil(t, byName["off"].Enabled)
}

func TestLoad_OneMalformedDoesNotStopTheOthers(t *testing.T) {
	// A single typo must not silently disarm a whole project.
	root := t.TempDir()
	writeGuardrail(t, root, "good-one", "---\nhooks: {}\n---\nfine\n")
	writeGuardrail(t, root, "good-two", "---\nhooks: {}\n---\nalso fine\n")
	writeGuardrail(t, root, "no-fence", "just prose, no frontmatter\n")
	writeGuardrail(t, root, "unterminated", "---\nhooks: {}\nnever closed\n")
	writeGuardrail(t, root, "bad-yaml", "---\nhooks: [unclosed\n---\nbody\n")

	decls, invalid, err := New(root).Load()
	require.NoError(t, err, "malformed declarations are reported, not fatal")

	names := make([]string, 0, len(decls))
	for _, d := range decls {
		names = append(names, d.Name)
	}
	assert.Equal(t, []string{"good-one", "good-two"}, names)

	badNames := make([]string, 0, len(invalid))
	for _, i := range invalid {
		badNames = append(badNames, i.Name)
		assert.NotEmpty(t, i.Reason, "an invalid declaration says why")
	}
	assert.Equal(t, []string{"bad-yaml", "no-fence", "unterminated"}, badNames)
}

func TestLoad_MissingDeclarationFileIsInvalidNotFatal(t *testing.T) {
	root := t.TempDir()
	writeGuardrail(t, root, "good", "---\nhooks: {}\n---\nbody\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "guardrails", "empty-folder"), 0o755))

	decls, invalid, err := New(root).Load()
	require.NoError(t, err)
	require.Len(t, decls, 1)
	require.Len(t, invalid, 1)
	assert.Equal(t, "empty-folder", invalid[0].Name)
	assert.Contains(t, invalid[0].Reason, "read:")
}

func TestLoad_IgnoresLooseFiles(t *testing.T) {
	// Only folders are guardrails; a stray file beside them is not an error.
	root := t.TempDir()
	writeGuardrail(t, root, "real", "---\nhooks: {}\n---\nbody\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "guardrails", "README.md"), []byte("notes"), 0o644))

	decls, invalid, err := New(root).Load()
	require.NoError(t, err)
	assert.Empty(t, invalid)
	require.Len(t, decls, 1)
	assert.Equal(t, "real", decls[0].Name)
}

func TestLoad_StableOrder(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"zebra", "alpha", "middle", "beta"} {
		writeGuardrail(t, root, n, "---\nhooks: {}\n---\nbody\n")
	}
	for _, n := range []string{"z-bad", "a-bad"} {
		writeGuardrail(t, root, n, "no fence\n")
	}

	for i := 0; i < 3; i++ {
		decls, invalid, err := New(root).Load()
		require.NoError(t, err)

		names := make([]string, 0, len(decls))
		for _, d := range decls {
			names = append(names, d.Name)
		}
		assert.Equal(t, []string{"alpha", "beta", "middle", "zebra"}, names)

		badNames := make([]string, 0, len(invalid))
		for _, iv := range invalid {
			badNames = append(badNames, iv.Name)
		}
		assert.Equal(t, []string{"a-bad", "z-bad"}, badNames)
	}
}

func TestLoad_BoundKinds(t *testing.T) {
	root := t.TempDir()
	writeGuardrail(t, root, "multi", `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./a.sh
  TurnEnd:
    - hooks:
        - type: command
          command: ./b.sh
---
body
`)
	decls, _, err := New(root).Load()
	require.NoError(t, err)
	require.Len(t, decls, 1)
	assert.ElementsMatch(t, []string{"PreFileCreate", "TurnEnd"}, decls[0].BoundKinds())
}

func TestBoundKinds_NoHooks(t *testing.T) {
	assert.Empty(t, Declaration{}.BoundKinds())
}

func TestLoad_AbsentMatcherIsEmptyString(t *testing.T) {
	// "Absent means every occurrence" — and an empty matcher string is what
	// CompileMatcher turns into the admits-everything matcher.
	root := t.TempDir()
	writeGuardrail(t, root, "unnarrowed", `---
hooks:
  TurnEnd:
    - hooks:
        - type: command
          command: ./a.sh
---
body
`)
	decls, _, err := New(root).Load()
	require.NoError(t, err)
	require.Len(t, decls, 1)

	b := decls[0].Hooks["TurnEnd"][0]
	assert.Empty(t, b.Matcher)

	m, err := CompileMatcher(b.Matcher)
	require.NoError(t, err)
	admitted, err := m.Match(fileEvent("anything"))
	require.NoError(t, err)
	assert.True(t, admitted)
}

// ---------------------------------------------------------------------------
// Loading WITH a registry: validation of what a declaration says it will do.
// ---------------------------------------------------------------------------

// writeGuardrail creates one guardrail folder with its declaration and an
// executable hook beside it, and returns the dot-directory root.
func writeGuardrailWithHook(t *testing.T, root, name, declaration string) string {
	t.Helper()
	dir := filepath.Join(root, "guardrails", name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "GUARDRAIL.md"), []byte(declaration), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok.sh"), []byte("#!/bin/sh\n"), 0o755))
	return root
}

const soundDeclaration = `---
hooks:
  PreFileCreate:
    - matcher: path startsWith "guarded/"
      hooks:
        - type: command
          command: ./ok.sh
---

# A rule that can do what it says
`

const misspelledField = `---
hooks:
  PreFileCreate:
    - matcher: pth startsWith "guarded/"
      hooks:
        - type: command
          command: ./ok.sh
---

# A rule that would never fire
`

const duplicatedKind = `---
hooks:
  PreFileCreate:
    - matcher: path startsWith "a/"
      hooks:
        - type: command
          command: ./ok.sh
  PreFileCreate:
    - matcher: path startsWith "b/"
      hooks:
        - type: command
          command: ./ok.sh
---

# A rule whose second binding collides with its first
`

func TestLoadWith_SoundDeclarationLoads(t *testing.T) {
	root := writeGuardrailWithHook(t, t.TempDir(), "sound", soundDeclaration)

	decls, invalid, err := New(root).LoadWith(testRegistry(t))
	require.NoError(t, err)
	assert.Empty(t, invalid)
	require.Len(t, decls, 1)
	assert.Equal(t, "sound", decls[0].Name)
}

func TestLoadWith_MatcherFaultMakesItInvalid(t *testing.T) {
	root := writeGuardrailWithHook(t, t.TempDir(), "typo", misspelledField)

	decls, invalid, err := New(root).LoadWith(testRegistry(t))
	require.NoError(t, err)
	assert.Empty(t, decls, "a rule that could never fire must not load as enforceable")
	require.Len(t, invalid, 1)
	assert.True(t, invalid[0].Has(ErrBadMatcher))
	assert.Contains(t, invalid[0].Reason, "pth")
}

// The isolation property, and the reason validation reports rather than
// refuses: a single typo must not silently disarm a whole project.
func TestLoadWith_OneBadDeclarationDisablesOnlyItself(t *testing.T) {
	root := t.TempDir()
	writeGuardrailWithHook(t, root, "sound", soundDeclaration)
	writeGuardrailWithHook(t, root, "typo", misspelledField)
	writeGuardrailWithHook(t, root, "dupe", duplicatedKind)

	decls, invalid, err := New(root).LoadWith(testRegistry(t))
	require.NoError(t, err)

	require.Len(t, decls, 1)
	assert.Equal(t, "sound", decls[0].Name, "the sound rule still enforces")

	require.Len(t, invalid, 2)
	assert.Equal(t, "dupe", invalid[0].Name)
	assert.Equal(t, "typo", invalid[1].Name, "reported in a stable order")
}

func TestLoadWith_DuplicateKindIsReported(t *testing.T) {
	root := writeGuardrailWithHook(t, t.TempDir(), "dupe", duplicatedKind)

	decls, invalid, err := New(root).LoadWith(testRegistry(t))
	require.NoError(t, err)
	assert.Empty(t, decls)
	require.Len(t, invalid, 1)
	assert.Contains(t, invalid[0].Reason, "PreFileCreate")
	assert.True(t, invalid[0].Has(ErrDuplicateKey))
}

// Reason and Reasons say the same thing, because one is derived from the other.
func TestLoadWith_ReasonSummarisesReasons(t *testing.T) {
	const twoFaults = `---
hooks:
  PreFileCreate:
    - matcher: pth startsWith "a/"
      hooks:
        - type: script
          command: ./ok.sh
---
`
	root := writeGuardrailWithHook(t, t.TempDir(), "two", twoFaults)

	_, invalid, err := New(root).LoadWith(testRegistry(t))
	require.NoError(t, err)
	require.Len(t, invalid, 1)

	assert.Len(t, invalid[0].Reasons, 2)
	for _, r := range invalid[0].Reasons {
		assert.Contains(t, invalid[0].Reason, r)
	}
}

// Load keeps working for callers with no registry: it still catches what it
// can, and does not invent kinds it has no way to know about.
func TestLoad_WithoutRegistrySkipsKindChecks(t *testing.T) {
	root := writeGuardrailWithHook(t, t.TempDir(), "typo", misspelledField)

	decls, invalid, err := New(root).Load()
	require.NoError(t, err)
	assert.Empty(t, invalid, "no registry means no kind to check the matcher against")
	assert.Len(t, decls, 1)
}

// A duplicate is a property of the file, so it is caught with or without a
// registry.
func TestLoad_WithoutRegistryStillCatchesDuplicates(t *testing.T) {
	root := writeGuardrailWithHook(t, t.TempDir(), "dupe", duplicatedKind)

	_, invalid, err := New(root).Load()
	require.NoError(t, err)
	require.Len(t, invalid, 1)
	assert.Contains(t, invalid[0].Reason, "PreFileCreate")
}

// A project that has not adopted sloprail is the ordinary case, not an error.
func TestLoadWith_NoDotDirectory(t *testing.T) {
	decls, invalid, err := New(filepath.Join(t.TempDir(), "absent")).LoadWith(testRegistry(t))
	require.NoError(t, err)
	assert.Empty(t, decls)
	assert.Empty(t, invalid)
}

func TestLoadWith_MissingFrontmatterIsReported(t *testing.T) {
	root := writeGuardrailWithHook(t, t.TempDir(), "prose-only", "# Just prose\n")

	_, invalid, err := New(root).LoadWith(testRegistry(t))
	require.NoError(t, err)
	require.Len(t, invalid, 1)
	assert.Contains(t, invalid[0].Reason, "frontmatter")
}

// ---------------------------------------------------------------------------
// A rule whose hook cannot run stays loaded.
//
// This is the load-time half of the fail-open guarantee. The runtime refuses an
// action whose hook exits non-zero, including the 126 of a file that is not
// executable — but only for a rule it was given. Dropping the rule here would
// mean nothing dispatches and the action proceeds, which is the bug by a
// different route.
// ---------------------------------------------------------------------------

const unrunnableHook = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./ok.sh
---

# Its hook exists but is not executable
`

func TestLoadWith_UnrunnableHookStillLoads(t *testing.T) {
	root := t.TempDir()
	writeGuardrailWithHook(t, root, "unrunnable", unrunnableHook)
	// Take away what makes it runnable, leaving the declaration untouched.
	require.NoError(t, os.Chmod(filepath.Join(root, "guardrails", "unrunnable", "ok.sh"), 0o644))

	decls, invalid, err := New(root).LoadWith(testRegistry(t))
	require.NoError(t, err)

	assert.Empty(t, invalid, "a chmod away from working is not a broken declaration")
	require.Len(t, decls, 1, "the rule must load, or nothing refuses the write it guards")

	require.Len(t, decls[0].Warnings, 1, "and it must say what is wrong")
	assert.ErrorIs(t, decls[0].Warnings[0], ErrHookNotRunnable)
	assert.Contains(t, decls[0].Warnings[0].Message(), "not executable")
}

// The same declaration with its hook executable loads clean, so the warning
// above is the check working rather than a warning nobody can clear.
func TestLoadWith_RunnableHookLoadsWithoutWarnings(t *testing.T) {
	root := writeGuardrailWithHook(t, t.TempDir(), "fine", unrunnableHook)

	decls, invalid, err := New(root).LoadWith(testRegistry(t))
	require.NoError(t, err)
	assert.Empty(t, invalid)
	require.Len(t, decls, 1)
	assert.Empty(t, decls[0].Warnings)
}

// A declaration fault still disqualifies, and takes the environment complaint
// with it into the report so the author sees both at once.
func TestLoadWith_DeclarationFaultStillDisqualifies(t *testing.T) {
	const bothFaults = `---
hooks:
  PreFileCreate:
    - matcher: pth startsWith "a/"
      hooks:
        - type: command
          command: ./ok.sh
---
`
	root := t.TempDir()
	writeGuardrailWithHook(t, root, "both", bothFaults)
	require.NoError(t, os.Chmod(filepath.Join(root, "guardrails", "both", "ok.sh"), 0o644))

	decls, invalid, err := New(root).LoadWith(testRegistry(t))
	require.NoError(t, err)
	assert.Empty(t, decls)
	require.Len(t, invalid, 1)

	assert.True(t, invalid[0].Has(ErrBadMatcher))
	assert.True(t, invalid[0].Has(ErrHookNotRunnable), "the chmod is reported too, not swallowed")
}
