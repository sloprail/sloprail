package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T041_71: the ways an agent can "touch" an uncited change to get a trailer onto the commit
// that last changed it. None of them is a real change, so none grounds anything: an empty
// commit, a blank line, a stripped final newline, CRLF line endings, an indent. (A real
// follow-up change that cites does ground the file: T041_70.)
func TestT041_71_NoTouchOfAnUncitedChangeGroundsIt(t *testing.T) {
	const original = "# log\nthe decision\n"
	touches := []struct {
		name string
		body string // what the touch writes; "" for an empty commit
	}{
		{name: "an empty commit with a trailer"},
		{name: "a blank line added", body: original + "\n"},
		{name: "the final newline stripped", body: "# log\nthe decision"},
		{name: "CRLF line endings", body: "# log\r\nthe decision\r\n"},
		{name: "an indent", body: "# log\n  the decision\n"},
		{name: "trailing spaces", body: "# log  \nthe decision\t\n"},
	}
	for i, tc := range touches {
		t.Run(tc.name, func(t *testing.T) {
			e, proj := guarded(t, afterCitationGuard)
			sess := "s-041-71-" + string(rune('a'+i))
			turns := []harness.Turn{
				Write("w1", "memories/a.md", original),
				harness.Commit("x", "write the decision"),
			}
			if tc.body != "" {
				turns = append(turns, Write("w2", "memories/a.md", tc.body))
			}
			turns = append(turns, harness.Commit("y", "touch", harness.CitesUser("adopt a decision log")))
			e.Run(proj, sess, prompt, Turns("done", turns...))
			blocks := stopRefusal(e, proj, sess)
			if !strings.Contains(blocks, noCitation) || !strings.Contains(blocks, "memories/a.md") {
				t.Fatalf("%s washed the earlier uncited change:\n%s", tc.name, blocks)
			}
		})
	}
}
