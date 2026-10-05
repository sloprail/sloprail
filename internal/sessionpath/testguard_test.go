package sessionpath

import (
	"path/filepath"
	"strings"
	"testing"
)

// A test process that resolves a data directory outside the OS temp dir (the developer's real
// Application Support / ~/.local/share) must be stopped, not served.
func TestDataHomeRefusesTheRealStoreInATest(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "/Users/someone")
	t.Setenv("LocalAppData", "")
	defer func() {
		r := recover()
		err, _ := r.(error)
		if err == nil || !strings.Contains(err.Error(), "real data directory") {
			t.Fatalf("DataHome with a real HOME did not panic with the guard's error: %v", r)
		}
	}()
	DataHome()
	t.Fatal("DataHome returned the real data directory inside a test")
}

func TestStateDBRefusesTheRealStoreInATest(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/Users/someone/Library/Application Support")
	defer func() {
		if recover() == nil {
			t.Fatal("StateDB resolved a path under the real store in a test without refusing")
		}
	}()
	StateDB(t.TempDir(), "s1")
}

func TestDataHomeAllowsATempStore(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", root)
	if got, err := DataHome(); err != nil || got != root {
		t.Fatalf("DataHome under a temp XDG_DATA_HOME = %q, %v", got, err)
	}

	// a fake HOME under the temp dir (what the e2e harness gives every process)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", root)
	t.Setenv("LocalAppData", root)
	if got, err := DataHome(); err != nil || !strings.HasPrefix(got, root) {
		t.Fatalf("DataHome under a temp HOME = %q, %v", got, err)
	}

	// a store that does not exist yet still counts as inside the temp dir
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "not", "yet"))
	if _, err := DataHome(); err != nil {
		t.Fatal(err)
	}
}
