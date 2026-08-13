package guardrail

import (
	"os"
	"path/filepath"
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

// TestSplitFrontmatter_FenceMatchingIsLoose pins CURRENT behaviour that is
// looser than the format the spec describes: the opening fence is matched with
// HasPrefix after trimming, so leading whitespace and trailing characters on
// the fence line are accepted. The closing fence, by contrast, is matched with
// Equal, so it must be exactly ---. This asymmetry is not documented anywhere.
func TestSplitFrontmatter_FenceMatchingIsLoose(t *testing.T) {
	t.Run("indented opening fence accepted", func(t *testing.T) {
		front, body, err := splitFrontmatter([]byte("   ---\na: 1\n---\nbody\n"))
		require.NoError(t, err)
		assert.Equal(t, "a: 1\n", string(front))
		assert.Equal(t, "body\n", string(body))
	})

	t.Run("four dashes accepted as opening fence", func(t *testing.T) {
		front, _, err := splitFrontmatter([]byte("----\na: 1\n---\nbody\n"))
		require.NoError(t, err)
		assert.Equal(t, "a: 1\n", string(front))
	})

	t.Run("opening fence with trailing text accepted", func(t *testing.T) {
		front, _, err := splitFrontmatter([]byte("---yaml\na: 1\n---\nbody\n"))
		require.NoError(t, err)
		assert.Equal(t, "a: 1\n", string(front))
	})

	t.Run("four dashes rejected as closing fence", func(t *testing.T) {
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
