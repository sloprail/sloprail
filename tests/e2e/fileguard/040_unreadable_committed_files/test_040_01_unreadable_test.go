package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// refuseUnreadable refuses, naming them, any changed file whose content as the
// rule is handed it is not ordinary text: a symlink (git holds its target path, so
// the rule sees "/dev/zero", never a stream of zeros) or a blob over a megabyte.
const refuseUnreadable = `#!/bin/sh
bad="$(jq -r '[.changeset.files[] | select((.newContent | length) > 1000000 or (.newContent | startswith("/dev/"))) | .path] | join(" ")')"
[ -z "$bad" ] && exit 0
printf '{"reason":"UNREADABLE: %s"}' "$bad"
exit 1
`

const docsRule = "match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n"

// maxStop is how long a Run and its check over these files may take. They are handled in
// seconds; a hang (reading a /dev/zero symlink) would be minutes, or forever.
const maxStop = 90 * time.Second

// T040_01: a committed file the rule cannot read as text is handled by the engine
// without hanging, and the range is refused, not waved through. A symlink to
// /dev/zero (a FIFO cannot be committed; this is the committable endless file) is
// read from git as the link it is; a 30 MB blob is handed over whole. The check
// refuses each by name; replacing them with ordinary files passes the same session.
func TestT040_01_UnreadableOrOversizedCommittedFilesFailClosed(t *testing.T) {
	cases := []struct {
		name, path, make string
	}{
		{"symlink-to-dev-zero", "docs/zero.md", "ln -s /dev/zero docs/zero.md"},
		{"oversized", "docs/big.md", "head -c 30000000 /dev/zero | tr '\\0' a > docs/big.md"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := New(t)
			proj := e.Project()
			e.GitInit(proj)
			e.WriteFile(proj, "docs/seed.md", "seed\n")
			e.CommitAll(proj, "the project")
			e.FileGuard(proj, "docs", docsRule, map[string]string{"check.sh": refuseUnreadable})
			e.CommitAll(proj, "the rule")
			sess := "s-040-01-" + c.name

			start := time.Now()
			e.Run(proj, sess, "add the file", Turns("done",
				Bash("m1", c.make),
				harness.Commit("c1", "add "+c.path),
			))
			if d := time.Since(start); d > maxStop {
				t.Fatalf("the run and check over %s took %v: the engine is reading something it should not", c.path, d)
			}
			if blocks := strings.Join(e.CheckRun(proj, sess), "\n"); !strings.Contains(blocks, "UNREADABLE: "+c.path) {
				t.Fatalf("%s was waved through or never handed to the rule: %q", c.path, blocks)
			}

			e.Run(proj, sess, "replace it with text", Turns("done",
				Bash("f1", "rm -f "+c.path),
				harness.CommitFile("f2", c.path, "ordinary text\n", "replace "+c.path),
			))
			if blocks := e.CheckRun(proj, sess); len(blocks) != 0 {
				t.Fatalf("the replaced file was still refused:\n%s", strings.Join(blocks, "\n"))
			}
		})
	}
}
