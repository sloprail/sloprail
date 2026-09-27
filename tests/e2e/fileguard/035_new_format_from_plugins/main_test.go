package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// new_format_from_plugins: a NEW-FORMAT guardrail (a file-guard) shipped inside an
// installed plugin is in force in a project that copied nothing — the new-format
// analogue of what 026 proves for the OLD format.
//
// Every test here drives the mock, so what fires is a plugin as a user installs
// it: the sloprail plugin's own hooks reach `sr-session pre-tool` and `stop`, and
// those run the nature dispatch, which resolves the SECOND enabled plugin from the
// project's settings (internal/harness) and loads its `.sloprail/file-guard`
// (declaration.NewWithPlugins). Nothing constructs a payload by hand, and nothing
// is written into the project's own `.sloprail`.
//
// The file-guard lives in a synthetic plugin rather than in the real shipped
// sloprail plugin, because the new-format migration of the shipped authoring-slop
// is a LATER PR — the mechanism under test here is the LOADING of a plugin-shipped
// NEW-format guardrail, and a purpose-built one proves it without depending on
// that migration. 026 continues to prove the OLD format's plugin loading against
// the real authoring-slop; the two run alongside each other.
var New = harness.New

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

var (
	Turns   = harness.Turns
	Write   = harness.Write
	Skill   = harness.Skill
	ToolUse = harness.ToolUse
)

// containsStr is a tiny local substring helper, so a test can assert on refusal
// text without importing strings in every file.
func containsStr(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
