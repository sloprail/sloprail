package dispatch

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// A guard that includes deletions can have its judge asked about a delete, whose
// event carries no newContent and no newMarkers. Under strict undefined a template
// that reads `event.newContent` without asking whether it is defined fails to
// render — and a judge that cannot render fails its check closed on every Stop, so
// the delete is refused forever. Every template of a guard that declares
// `deletions: include` or `only` must render against a delete.
func TestRealExampleTemplatesRenderOnADelete(t *testing.T) {
	root := repoTemplatesRoot(t)
	templates := append(findTemplates(t, root), findTemplates(t, filepath.Join(root, "..", "marketplace", "plugins"))...)

	vars := assembledJudgeVars(t)
	ev := map[string]any{}
	for k, v := range vars["event"].(map[string]any) {
		ev[k] = v
	}
	delete(ev, "newContent")
	delete(ev, "newMarkers")
	ev["kind"] = "PostFileDelete"
	deleteVars := map[string]any{}
	for k, v := range vars {
		deleteVars[k] = v
	}
	deleteVars["event"] = ev

	includesDeletions := regexp.MustCompile(`(?m)^deletions:\s*(include|only)\b`)
	n := 0
	for _, path := range templates {
		guard, err := os.ReadFile(filepath.Join(filepath.Dir(path), "file-guard.yaml"))
		if err != nil || !includesDeletions.Match(guard) {
			continue
		}
		n++
		t.Run(filepath.Base(filepath.Dir(path))+"/"+filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			require.NoError(t, err)
			_, err = renderTemplate(string(src), deleteVars)
			require.NoErrorf(t, err, "template %s must render on a delete, or its guard refuses every delete forever", path)
		})
	}
	require.NotZero(t, n, "no template of a deletion-including guard was found — the walk is wrong")
}
