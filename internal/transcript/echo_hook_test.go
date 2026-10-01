package transcript

import (
	"encoding/json"
	"os"
	"testing"
)

// The package's own tests run with a classifier that calls nothing an echo (the real one
// lives in commandmod, which imports this package); TestUnsetEchoHookFailsClosed covers
// the default.
func TestMain(m *testing.M) {
	CommandEchoes = func(string) bool { return false }
	os.Exit(m.Run())
}

func TestUnsetEchoHookFailsClosed(t *testing.T) {
	saved := CommandEchoes
	t.Cleanup(func() { CommandEchoes = saved })
	CommandEchoes = nil
	bash := recordCall{assistantContentBlock: assistantContentBlock{Name: "Bash", Input: json.RawMessage(`{"command":"echo ok"}`)}}
	if !echoesRecord(bash) {
		t.Fatal("with no classifier registered a Bash result must not be citable")
	}
	CommandEchoes = func(string) bool { return false }
	if echoesRecord(bash) {
		t.Fatal("a registered classifier that says no must admit the result")
	}
}
