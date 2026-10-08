// Package tuidrive drives a harness's interactive (TUI) mode headlessly, on a
// pseudo-terminal: it waits for the input to be live, types a prompt, decides
// when the turn has ended, and quits the way a person does.
//
// It exists because some harness hooks fire only in the interactive mode
// (Cursor's stop, beforeSubmitPrompt and afterAgentResponse never fire in
// `cursor-agent -p`), so an agent run for evaluation that should exercise those
// hooks must be a TUI session. Everything it knows about one harness's screen
// arrives in a Spec the harness supplies (internal/harness.Interactive); this
// package names no harness.
//
// # How a turn is known to have ended
//
// A TUI draws a screen and prints no "done". What a headless driver can observe
// is the session record the harness writes, the screen, and the processes the
// harness has running (a stop hook, a shell tool, a judge launched by a hook are
// all children of it). A turn has ended when, after its prompt was accepted (the
// record changed), the record and the screen have both been quiet for the settle
// time, and the harness has no process running that it did not already have when
// the prompt was typed. A stop hook that asks the agent to go on writes the
// follow-up into the record at once, so it restarts the quiet period instead of
// ending the turn; a stop hook that is still running keeps a process alive, which
// is why the settle time is longer while one is (Options.ProcSettle) and why a
// quiet record alone is never taken for the end.
package tuidrive
