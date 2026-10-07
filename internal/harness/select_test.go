package harness

import (
	"strings"
	"testing"
)

type fakeHarness struct {
	Harness
	name   string
	detect string // an environment variable that marks this harness
}

func (f fakeHarness) Name() string { return f.name }

type detectingHarness struct{ fakeHarness }

func (d detectingHarness) Detect(environ []string) bool {
	for _, kv := range environ {
		if strings.HasPrefix(kv, d.detect+"=") {
			return true
		}
	}
	return false
}

func reset(t *testing.T, hs ...Harness) {
	t.Helper()
	mu.Lock()
	old := registry
	registry = map[string]Harness{}
	mu.Unlock()
	for _, h := range hs {
		Register(h)
	}
	t.Cleanup(func() { mu.Lock(); registry = old; mu.Unlock() })
}

func TestSelect_ExplicitNameWinsOverDetectionAndDefault(t *testing.T) {
	reset(t, fakeHarness{name: Default}, detectingHarness{fakeHarness{name: "alpha", detect: "ALPHA"}}, fakeHarness{name: "beta"})
	if got := Select([]string{"ALPHA=1", SelectEnv + "=beta"}).Name(); got != "beta" {
		t.Fatalf("got %q, want the harness SelectEnv names", got)
	}
}

func TestSelect_DetectionBeatsDefaultAndDefaultIsTheFallback(t *testing.T) {
	reset(t, fakeHarness{name: Default}, detectingHarness{fakeHarness{name: "alpha", detect: "ALPHA"}})
	if got := Select([]string{"ALPHA=1"}).Name(); got != "alpha" {
		t.Fatalf("got %q, want the detected harness", got)
	}
	if got := Select([]string{"PATH=/bin"}).Name(); got != Default {
		t.Fatalf("got %q, want the default when nothing selects another", got)
	}
}

func TestSelect_AnUnknownExplicitNamePanics(t *testing.T) {
	reset(t, fakeHarness{name: Default})
	defer func() {
		if recover() == nil {
			t.Fatal("a misspelt SelectEnv fell back instead of failing")
		}
	}()
	Select([]string{SelectEnv + "=nope"})
}

func TestSelect_NoneRegisteredPanics(t *testing.T) {
	reset(t)
	defer func() {
		if recover() == nil {
			t.Fatal("no harness registered did not panic")
		}
	}()
	Select(nil)
}

func TestSelect_TheOnlyRegisteredHarnessIsUsedWhateverItsName(t *testing.T) {
	reset(t, fakeHarness{name: "solo"})
	if got := Select(nil).Name(); got != "solo" {
		t.Fatalf("got %q", got)
	}
}
