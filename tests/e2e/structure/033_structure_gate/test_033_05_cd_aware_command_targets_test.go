package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// T033_05 and T033_06 are the structure gate's own end-to-end proof of the
// cd-aware command-target fix.
//
// The bug: a Bash command that `cd`s into a DIFFERENT directory and then
// writes a RELATIVE path in the same command line was resolved against the
// SESSION's own project root rather than the directory the command actually
// `cd`'d into — so a write to some other repository entirely was reported (and
// refused, or permitted) as though it named a file inside the current
// project's own structure allowlist. See internal/commandmod/cwd.go for the
// fix and internal/filemod's extract_command_test.go for the module-level
// pins; these two tests are the same claim proven through the compiled
// sr-session binary and a REAL structure gate, which is where the defect was
// originally measured.

// outsideProjectStructureYAML is structureYAML plus the one entry that makes
// an outside-the-project write representable at all: an explicit `regex:
// '^/'` allow, which is what admits an ABSOLUTE path — deny-by-default
// otherwise refuses one exactly as it refuses any other unlisted path, and
// correctly so; the structure gate has no opinion of its own about a
// directory that is not the project, and admitting one is an explicit choice
// an author makes with this entry, not something cd-awareness should imply on
// its own. See the doc comment on T033_05 below for why this test needs it and
// T033_06 does not.
const outsideProjectStructureYAML = structureYAML + `  - regex: "^/"
`

// T033_05: a command that `cd`s OUTSIDE the project and writes a relative path
// there must be resolved as a write to THAT directory — not reported as
// though it named a path inside the project's own tree, which is what the bug
// did.
//
// The structure gate here explicitly allows outside-the-project absolute
// paths (outsideProjectStructureYAML), so what this test isolates is purely
// the RESOLUTION question this task fixes, not the separate policy question
// of whether an outside write should be allowed at all — T033_01 and this
// package's deny-by-default default already cover that a project's structure
// gate has no obligation to admit paths outside itself.
//
// Before the fix, `cd <B> && cat > src/x.txt` reported `src/x.txt` — a bare
// relative path — which the caller resolved against PROJECT A's own root,
// landing it inside A's tree and refusing it there (deny-by-default; A's
// structure.yaml allows nothing under src/, the same allowlist T033_01
// proves refuses `src/main.go` written directly) even though the command
// never touches A's src/ at all.
func TestT033_05_CdOutOfProjectRelativeWriteIsResolvedWhereTheCommandActuallyCds(t *testing.T) {
	e := New(t)
	projA := e.Project()
	e.StructureGate(projA, outsideProjectStructureYAML)

	repoB := t.TempDir()

	res := e.Run(projA, "s-033-05", "write into another repo entirely", Turns("done",
		Bash("w1", "cd "+repoB+" && mkdir -p src && cat > src/x.txt <<'EOF'\nhello\nEOF\n"),
	))

	if res.Refused() {
		t.Errorf("a write that cd'd OUT of the project and wrote a relative path there "+
			"was refused, even though the structure gate explicitly allows outside paths:\n%s", res.Output)
	}
	if e.Exists(projA, "src/x.txt") {
		t.Errorf("the write landed inside the PROJECT's own tree — " +
			"it should have landed in the other repo the command cd'd into")
	}
	if _, err := os.Stat(filepath.Join(repoB, "src", "x.txt")); err != nil {
		t.Errorf("the write did not land where the command actually cd'd to (%s): %v", repoB, err)
	}
}

// T033_06 is the negative control in the SAME file T033_05 lives in, and the
// one that proves the fix did not become globally permissive.
//
// A `cd` that stays INSIDE the project, followed by a relative write to a path
// the project's OWN structure gate does not allow, must still be refused. If
// the fix above were achieved by simply no longer resolving cd-affected
// relative paths against the project root AT ALL — rather than resolving them
// against the CORRECT effective directory — this is the case that would
// silently start passing everything through.
func TestT033_06_CdWithinTheProjectStillHonoursTheStructureGate(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.StructureGate(proj, structureYAML)

	res := e.Run(proj, "s-033-06", "cd within the project and write somewhere forbidden", Turns("done",
		Bash("w1", "cd "+proj+" && mkdir -p forbidden && cat > forbidden/not-allowed.txt <<'EOF'\nno\nEOF\n"),
	))

	if !res.Refused() {
		t.Errorf("a cd that stayed INSIDE the project, writing to a path outside its "+
			"structure allowlist, was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, "forbidden/not-allowed.txt") {
		t.Errorf("the forbidden write LANDED — cd-awareness must not widen the allowlist")
	}
}
