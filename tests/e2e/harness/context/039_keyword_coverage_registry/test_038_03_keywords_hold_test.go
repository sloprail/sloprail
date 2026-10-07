package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// readScanner is the declared scanner as it stands on disk.
func readScanner(t *testing.T, proj string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(proj, "scanners", "mine", "scanner.yaml"))
	if err != nil {
		t.Fatalf("read the scanner: %v", err)
	}
	return string(b)
}
