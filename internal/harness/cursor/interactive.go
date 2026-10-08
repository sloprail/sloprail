package cursor

import "github.com/sloprail/sloprail/internal/harness"

// HooksNeedInteractive implements harness.Interactive: `cursor-agent -p` never fires the
// stop hook, and beforeSubmitPrompt and afterAgentResponse are TUI-only too (recorded in
// harness-mocks: runs/tui-stop, tui-stop-followup, tui-prompt-blocked, none fired in print
// mode), so a run in `-p` exercises none of sloprail's Stop rules or the stop follow-up
// loop.
func (Harness) HooksNeedInteractive() bool { return true }

// TUI implements harness.Interactive. Both facts are what the recordings of the real TUI
// drove (cursor-mock/snapshots/runs/tui-*/setup/tui.yaml, made with tools/tui-record
// against cursor-agent 2026.09.28):
//
//   - the input is live once the screen shows its "Plan, search, build anything."
//     placeholder and has switched bracketed paste on (CSI ? 2004 h); what is typed before
//     that is lost, though the screen is already drawn;
//   - the session ends with Ctrl+C, the screen saying "Press Ctrl+C again to exit", and
//     Ctrl+C again (exit status 0, sessionEnd fired).
//
// The idle placeholder after a turn ("Add a follow-up <model>") is deliberately not a fact
// here: it names the model, and no recording shows it absent while a turn is running, so
// it is not known to say the turn is over (the driver ends a turn on the record, the
// screen and the processes instead; see internal/tuidrive).
func (Harness) TUI() harness.TUI {
	return harness.TUI{
		Ready: `Plan, search, build anything.*<\?2004h>`,
		Quit: []harness.TUIQuit{
			{Keys: []string{"ctrl-c"}, Expect: `Press Ctrl\+C again to exit`},
			{Keys: []string{"ctrl-c"}},
		},
	}
}
