package tuidrive

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The tests drive a stand-in TUI: this test binary run again with fakeEnv set. It behaves as
// the recordings of Cursor's real TUI do (harness-mocks cursor-mock/snapshots/runs/tui-*):
// "Plan, search, build anything." drawn with bracketed paste switched on once its input is
// live, a prompt typed or pasted and submitted with Enter, a record of the conversation
// written to a file, a stop hook run after each answer (a process, silent on the screen and
// in the record), that hook's follow-up taken as the next message, and Ctrl+C twice to exit.
const fakeEnv = "TUIDRIVE_FAKE_TUI"

func TestMain(m *testing.M) {
	if os.Getenv(fakeEnv) != "" {
		fakeTUI()
		return
	}
	os.Exit(m.Run())
}

func envMs(name string) time.Duration {
	n, _ := strconv.Atoi(os.Getenv(name))
	return time.Duration(n) * time.Millisecond
}

func appendRecord(path string, v map[string]any) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(v)
	_, _ = f.Write(append(b, '\n'))
}

func text(role, s string) map[string]any {
	return map[string]any{"role": role, "text": s}
}

func fakeTUI() {
	record := os.Getenv("FAKE_RECORD")
	stty := exec.Command("stty", "raw", "-echo")
	stty.Stdin = os.Stdin
	_ = stty.Run()

	if os.Getenv("FAKE_NEVER_READY") == "" {
		fmt.Print("Plan, search, build anything.\x1b[?2004h")
	}
	var typed strings.Builder
	paste, ctrlC := false, false
	buf := make([]byte, 4096)
	pending := ""
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return
		}
		pending += string(buf[:n])
		for pending != "" {
			switch {
			case strings.HasPrefix(pending, "\x1b[200~"):
				paste, pending = true, pending[6:]
			case strings.HasPrefix(pending, "\x1b[201~"):
				paste, pending = false, pending[6:]
			case pending[0] == 3:
				pending = pending[1:]
				if os.Getenv("FAKE_IGNORE_QUIT") != "" {
					continue
				}
				if !ctrlC {
					ctrlC = true
					fmt.Print("Press Ctrl+C again to exit")
					continue
				}
				return
			case pending[0] == '\r' && !paste:
				pending = pending[1:]
				prompt := typed.String()
				typed.Reset()
				fakeTurn(record, prompt)
			default:
				typed.WriteByte(pending[0])
				fmt.Print(string(pending[0]))
				pending = pending[1:]
			}
		}
	}
}

// fakeTurn is one prompt: the user's record, the agent's work (silent on the screen when
// FAKE_SILENT_MS says so), its answer, then the stop hook as a process, which asks for one
// follow-up when FAKE_FOLLOWUP is set.
func fakeTurn(record, prompt string) {
	if os.Getenv("FAKE_EXIT_ON_PROMPT") != "" {
		os.Exit(3)
	}
	appendRecord(record, text("user", prompt))
	time.Sleep(envMs("FAKE_WORK_MS"))
	if d := envMs("FAKE_TOOL_MS"); d > 0 { // a tool call, whose outcome the record never holds
		appendRecord(record, map[string]any{"role": "assistant", "tool_use": "Shell"})
		time.Sleep(d)
	}
	appendRecord(record, text("assistant", "done: "+strings.SplitN(prompt, "\n", 2)[0]))
	fmt.Print("\r\nANSWERED\r\n")
	if d := envMs("FAKE_HOOK_MS"); d > 0 {
		hook := exec.Command("sleep", strconv.FormatFloat(d.Seconds(), 'f', 3, 64))
		_ = hook.Run()
		if os.Getenv("FAKE_FOLLOWUP") != "" {
			appendRecord(record, text("user", "FOLLOWUP"))
			time.Sleep(50 * time.Millisecond)
			appendRecord(record, text("assistant", "done: FOLLOWUP"))
		}
	}
}
