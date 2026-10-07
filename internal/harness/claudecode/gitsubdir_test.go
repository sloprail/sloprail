package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

// The shipped marketplace.json pins each plugin to a release tag with a
// git-subdir source object. A directory-sourced marketplace must still resolve
// to the checkout's own plugin directory, never to anything the manifest's
// source names: that is what keeps the e2e harness and local development on
// the working tree rather than the released tag.
func TestResolve_DirectoryMarketplaceIgnoresGitSubdirPins(t *testing.T) {
	home := t.TempDir()
	src := t.TempDir()
	want := filepath.Join(src, "marketplace", "plugins", "acme")
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"acme-marketplace","plugins":[{"name":"acme","source":{"source":"git-subdir","url":"o/r","path":"marketplace/plugins/acme","ref":"v1.0.0"}}]}`
	if err := os.WriteFile(filepath.Join(src, ".claude-plugin", "marketplace.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := `{"enabledPlugins":{"acme@acme-marketplace":true},` +
		`"extraKnownMarketplaces":{"acme-marketplace":{"source":{"source":"directory","path":"` + src + `"}}}}`
	res, err := Resolve(project(t, map[string]string{"settings.json": settings}), home)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Roots) != 1 || res.Roots[0].Dir != want {
		t.Fatalf("roots=%+v unresolved=%+v, want %s", res.Roots, res.Unresolved, want)
	}
}
