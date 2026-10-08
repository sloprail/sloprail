package harness

import (
	"strings"
	"testing"
)

// A megabyte line in the test log stalls CI's log pipe until the job is cancelled, so
// the logged copy of the mock's output clips long lines and keeps the short ones whole.
func TestClipLongLinesBoundsOnlyTheLongOnes(t *testing.T) {
	long := strings.Repeat("A", 10_000)
	got := clipLongLines("short\n"+long+"\nalso short", 100)
	lines := strings.Split(got, "\n")
	if len(lines) != 3 || lines[0] != "short" || lines[2] != "also short" {
		t.Fatalf("short lines were changed: %q", lines)
	}
	if len(lines[1]) > 200 || !strings.Contains(lines[1], "9900 more bytes not logged") {
		t.Fatalf("the long line was not clipped, or does not say how much it dropped: %q", lines[1])
	}
}
