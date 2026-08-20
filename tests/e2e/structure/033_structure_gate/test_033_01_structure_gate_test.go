package e2e

import (
	"testing"
)

// The structure.yaml every test in this group installs: writes are allowed under
// memories/updates/*.md (a glob) and under a dated decisions folder (a regex), and
// nowhere else — the exact shape examples/completeness-artifact-on-trigger ships.
const structureYAML = `allow:
  - glob: "memories/updates/*.md"
  - regex: "^memories/decisions/[0-9]{8}_[a-z0-9-]+/.*\\.md$"
`

// T033_01: a write OUTSIDE the allowlist is blocked before it lands.
//
// The whole point of deny-by-default: a path matching no allow entry is refused,
// and — the claim a test about prevention must actually check — the file does not
// exist afterwards. Reading the stream for a refusal marker is not enough; the tree
// is what says the write was prevented.
func TestT033_01_WriteOutsideAllowlistIsBlocked(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.StructureGate(proj, structureYAML)

	res := e.Run(proj, "s-033-01", "write somewhere forbidden", Turns("done",
		Write("w1", "src/main.go", "package main"),
	))

	if !res.Refused() {
		t.Errorf("a write outside the structure allowlist was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, "src/main.go") {
		t.Errorf("the forbidden write LANDED — the structure gate did not prevent it")
	}
}

// T033_02: a write INSIDE the allowlist (matching the glob) is permitted.
//
// The control for T033_01. Without it, a structure gate that refused EVERYTHING
// would pass T033_01 while being broken — the glob would prove nothing.
func TestT033_02_WriteMatchingGlobIsAllowed(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.StructureGate(proj, structureYAML)

	res := e.Run(proj, "s-033-02", "write an allowed update", Turns("done",
		Write("w1", "memories/updates/note.md", "# a note"),
	))

	if res.Refused() {
		t.Errorf("a write matching an allow glob was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, "memories/updates/note.md") {
		t.Errorf("the allowed write did not land")
	}
}

// T033_03: a write matching the REGEX allow entry is permitted.
//
// The second allow entry is a regex, for the dated-folder shape a glob cannot
// express. This proves both entry kinds work in one gate — the glob (T033_02) and
// the regex here.
func TestT033_03_WriteMatchingRegexIsAllowed(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.StructureGate(proj, structureYAML)

	res := e.Run(proj, "s-033-03", "write a dated decision", Turns("done",
		Write("w1", "memories/decisions/20260101_the-call/notes.md", "# the decision"),
	))

	if res.Refused() {
		t.Errorf("a write matching the allow regex was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, "memories/decisions/20260101_the-call/notes.md") {
		t.Errorf("the regex-allowed write did not land")
	}
}

// T033_04: a deny exception carves a path back OUT of what allow permitted.
//
// A broad allow (everything under memories/) with a deny for memories/secret/ — a
// path under the allow but inside the deny is refused, while a sibling under the
// allow but outside the deny still passes. This is the deny-subtracts-from-allow
// semantics the spec fixes.
func TestT033_04_DenyCarvesException(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.StructureGate(proj, `allow:
  - glob: "memories/**/*.md"
deny:
  - glob: "memories/secret/*.md"
`)

	// Under allow but carved out by deny — refused, and does not land.
	blocked := e.Run(proj, "s-033-04a", "write a secret", Turns("done",
		Write("w1", "memories/secret/keys.md", "shhh"),
	))
	if !blocked.Refused() {
		t.Errorf("a write under a deny exception was not refused:\n%s", blocked.Output)
	}
	if e.Exists(proj, "memories/secret/keys.md") {
		t.Errorf("the deny-excepted write LANDED — the exception did not carve it out")
	}

	// Under allow and NOT under deny — permitted, and lands.
	allowed := e.Run(proj, "s-033-04b", "write an ordinary memory", Turns("done",
		Write("w2", "memories/topics/a.md", "# a topic"),
	))
	if allowed.Refused() {
		t.Errorf("a write under allow but outside the deny was refused:\n%s", allowed.Output)
	}
	if !e.Exists(proj, "memories/topics/a.md") {
		t.Errorf("a write the deny does not cover did not land")
	}
}
