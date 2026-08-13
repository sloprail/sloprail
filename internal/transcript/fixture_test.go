package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

// Fixtures are real transcript files in a real directory, not fakes. What is
// being tested is the reading of a harness's own record and the walk across the
// files it writes — a fake would define away the two things that actually go
// wrong: what a line looks like, and which file a uuid is in.

// project is one harness project directory holding a conversation's transcripts.
type project struct {
	t   *testing.T
	dir string
}

func newProject(t *testing.T) *project {
	t.Helper()
	// The encoded directory name is a 1:1 substitution with no shortening, so a
	// working directory under a long test name would push one component past
	// the filename limit. A plain temp dir keeps the fixture honest without
	// courting that.
	dir := t.TempDir()
	return &project{t: t, dir: dir}
}

// write puts a transcript of the given lines at <dir>/<name>.jsonl and returns
// its path.
func (p *project) write(name string, lines ...string) string {
	p.t.Helper()
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	path := filepath.Join(p.dir, name+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		p.t.Fatalf("fixture: write %s: %v", path, err)
	}
	return path
}

// The record shapes below are the ones Claude Code actually writes, taken from
// real transcripts rather than invented: a preamble line carrying no uuid, a
// root record whose parentUuid is JSON null, an ordinary record naming its
// parent, and a compact_boundary whose parentUuid is null AND which carries a
// logicalParentUuid into an older file.

func preamble() string {
	return `{"type":"custom-title","customTitle":"a conversation","sessionId":"s"}`
}

func root(uuid string) string {
	return `{"type":"user","uuid":"` + uuid + `","parentUuid":null,"isSidechain":false,` +
		`"timestamp":"2026-08-13T09:00:00.000Z","message":{"role":"user","content":"start"}}`
}

func record(uuid, parent string) string {
	return `{"type":"assistant","uuid":"` + uuid + `","parentUuid":"` + parent + `","isSidechain":false,` +
		`"timestamp":"2026-08-13T09:01:00.000Z","message":{"role":"assistant","content":"working"}}`
}

// boundary is the first record of a transcript opened after a restart or a
// compaction: nothing in this file precedes it, so parentUuid is null — yet it
// is not a beginning, which is what logicalParentUuid says.
func boundary(uuid, logicalParent string) string {
	return `{"type":"system","subtype":"compact_boundary","uuid":"` + uuid + `","parentUuid":null,` +
		`"logicalParentUuid":"` + logicalParent + `","isSidechain":false,` +
		`"timestamp":"2026-08-13T10:00:00.000Z"}`
}
