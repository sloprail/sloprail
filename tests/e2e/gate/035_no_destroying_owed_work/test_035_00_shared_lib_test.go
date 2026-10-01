package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T035_00: every script of the two gates parses (a `when` that cannot even parse fails the
// gate closed, with no hint, which reads like the gate working).
func TestT035_00_TheGateScriptsParse(t *testing.T) {
	root := filepath.Join(harness.ShippedSkillFile(t, ""), "..", "..", ".sloprail", "gate")
	for _, f := range []string{
		"no-merge-over-refusals/open-refusals.sh", "no-merge-over-refusals/owed-work.sh",
		"no-destroying-owed-work/destroys-owed.sh", "no-destroying-owed-work/owed-work.sh",
	} {
		if out, err := exec.Command("bash", "-n", filepath.Join(root, f)).CombinedOutput(); err != nil {
			t.Errorf("%s does not parse: %v\n%s", f, err, out)
		}
	}
}

// T035_00: the two gates that decide what is "owed" each carry their own copy of the shared
// owed-work.sh (a gate's folder is all it can rely on); the copies must never drift.
func TestT035_00_TheSharedOwedWorkLibraryIsIdenticalInBothGates(t *testing.T) {
	root := filepath.Join(harness.ShippedSkillFile(t, ""), "..", "..", ".sloprail", "gate")
	a, err := os.ReadFile(filepath.Join(root, "no-merge-over-refusals", "owed-work.sh"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "no-destroying-owed-work", "owed-work.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("owed-work.sh differs between no-merge-over-refusals and no-destroying-owed-work; copy one over the other")
	}
}
