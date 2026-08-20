package dispatch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every .md.j2 template the spec's examples ship must render through this engine
// — not merely trimmed copies of them. This walks the real files and renders each
// against a permissive variable set, asserting only that the renderer does not
// REFUSE a template a real example carries. It is what keeps the supported subset
// honest against the templates actually written: a construct an example uses that
// this engine cannot render would fail a judge closed forever, so it must fail
// this test loudly instead.
//
// The point is the parse/render succeeding, not a particular output — the inputs
// are stand-ins, and which branch renders is not what is under test here (the
// per-construct tests cover that). A template that needs a construct beyond the
// subset shows up as a render error, which is exactly the signal to widen the
// subset or reconsider the template.

func TestRealExampleTemplatesRender(t *testing.T) {
	root := repoTemplatesRoot(t)
	templates := findTemplates(t, root)
	require.NotEmpty(t, templates, "no .md.j2 templates found under %s — the walk or the path is wrong", root)

	// A permissive variable world: the payload fields the templates read, with
	// additionalContext carrying the keys the prepare-fed ones use. Missing keys
	// are undefined (nil), which the templates' own guards handle.
	vars := map[string]any{
		"event": map[string]any{
			"path":       "some/file.md",
			"newContent": "the new content",
			"oldContent": "the old content",
			"newMarkers": []any{map[string]any{"kind": "conforms-to-doc", "fqn": "F", "line": float64(3)}},
			"oldMarkers": []any{},
		},
		"transcriptPath": "/rec.jsonl",
		"context":        map[string]any{},
		"additionalContext": map[string]any{
			"action_taken": true,
			"action":       "fill_form",
			"action_input": "{}",
			"proof":        "a screenshot",
			"resolved":     true,
			"citations": []any{
				map[string]any{"quote": "q", "source_excerpt": "s", "resolves": true},
			},
			"doc_text":  "doc",
			"code_text": "code",
		},
	}

	for _, path := range templates {
		t.Run(filepath.Base(filepath.Dir(path))+"/"+filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			require.NoError(t, err)
			out, err := renderTemplate(string(src), vars)
			require.NoErrorf(t, err, "template %s must render through this engine, or the judge fails closed forever", path)
			assert.NotEmpty(t, out, "a rendered judge prompt should not be empty")
		})
	}
}

// repoTemplatesRoot finds the examples directory holding the .md.j2 templates.
func repoTemplatesRoot(t *testing.T) string {
	t.Helper()
	// This test file sits at internal/dispatch/; the examples are at the repo root.
	wd, err := os.Getwd()
	require.NoError(t, err)
	root := filepath.Join(wd, "..", "..", "examples")
	if _, err := os.Stat(root); err != nil {
		t.Skipf("examples directory not found at %s: %v", root, err)
	}
	return root
}

// findTemplates lists every .md.j2 under root, excluding the deprecated tree.
func findTemplates(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "deprecated" {
			return filepath.SkipDir
		}
		if !d.IsDir() && filepath.Ext(path) == ".j2" {
			out = append(out, path)
		}
		return nil
	})
	require.NoError(t, err)
	return out
}
