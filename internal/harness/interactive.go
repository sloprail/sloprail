package harness

// Interactive is what a Harness MAY implement when some of its hooks fire only in its
// interactive (TUI) mode, not in the one-shot mode a headless launcher uses by default.
//
// Cursor is the case: `cursor-agent -p` never fires its stop hook, and its
// beforeSubmitPrompt and afterAgentResponse hooks are TUI-only too (recorded in
// harness-mocks, runs/tui-*), so a run in `-p` never exercises a Stop rule, the stop
// follow-up loop or a prompt gate. A launcher that must reproduce real use asks the
// harness (rather than naming it) and, when the answer is yes, runs the agent under test
// in the TUI, driven on a pseudo-terminal.
type Interactive interface {
	// HooksNeedInteractive reports whether the hooks sloprail relies on fire only in the
	// interactive mode.
	HooksNeedInteractive() bool

	// TUI is how the interactive mode is driven.
	TUI() TUI
}

// TUI is what a harness says of its interactive screen: when the input is live and how
// the session is quit. It is data, so the harness package can hold it without the driver.
type TUI struct {
	// Ready is a regexp over the screen text (escape sequences dropped, whitespace
	// collapsed; a private terminal mode being set shows as a token such as "<?2004h>")
	// that matches once the input is live. A prompt typed before then is lost.
	Ready string

	// Quit are the steps that end the session, in order.
	Quit []TUIQuit

	// Rows and Cols are the terminal's size; zero means the driver's default.
	Rows, Cols uint16
}

// TUIQuit presses Keys (named: enter, esc, ctrl-c, ctrl-d, ...) and then, when Expect is
// set, waits for that screen regexp: a harness that asks for a second Ctrl+C says so.
type TUIQuit struct {
	Keys   []string
	Expect string
}
