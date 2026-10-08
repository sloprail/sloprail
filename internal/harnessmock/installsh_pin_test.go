package harnessmock

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestInstallShDefaultMatchesThePin: install.sh run from a release has no
// version.txt beside it, so its default IS the mock it installs; a default
// behind the pin installed a mock sr-test then refused (or none at all, when
// that old release had no build for the platform).
func TestInstallShDefaultMatchesThePin(t *testing.T) {
	for _, p := range []string{"../../install.sh", "../../marketplace/plugins/sloprail/hooks/install.sh"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		m := regexp.MustCompile(`(?m)^HARNESS_MOCK_DEFAULT_VERSION="([^"]+)"`).FindSubmatch(b)
		if m == nil {
			t.Fatalf("%s: no HARNESS_MOCK_DEFAULT_VERSION", p)
		}
		if got, want := string(m[1]), strings.TrimPrefix(Version, "v"); got != want {
			t.Errorf("%s installs a10n-claude-mock %s, but sr-test is pinned to %s (internal/harnessmock/version.txt)", p, got, Version)
		}
	}
}
