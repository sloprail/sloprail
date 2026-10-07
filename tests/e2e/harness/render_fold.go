package harness

// foldCallOutput is how Codex and Cursor take a call whose output the scenario states
// (CallWithOutput: an ActCall of the shell, then an ActToolResult of the same id).
//
// Claude's mock cannot run the command the call names (or runs it for a result of its own),
// so the scenario supplies the result record separately. Codex and Cursor run their shell for
// real, and what the call returns is what they record as its result: so the pair is ONE shell
// command that prints the stated output, and the result record that follows is the harness's
// own, holding that output for the call that made it. The result is equivalent for what these
// scenarios use it for (a tool_result whose call is in the record, with this text): the text a
// later citation quotes is the command's real output, not the command's text, and
// `printf '%s'` adds nothing to it.
//
// A result that has no call before it (ToolResult alone) is not folded: neither harness can
// hold a result whose call is not in its record.
func foldCallOutput(turns []Turn) []Turn {
	out := make([]Turn, 0, len(turns))
	for i := 0; i < len(turns); i++ {
		a := turns[i].act
		if a.Kind == ActCall && a.Tool == "Bash" && a.Raw == "" && a.Input["command"] != "" && i+1 < len(turns) {
			if r := turns[i+1].act; r.Kind == ActToolResult && r.ID == a.ID {
				out = append(out, Turn{act: Action{Kind: ActBash, ID: a.ID, Command: "printf '%s' " + shQuote(r.Text)}})
				i++
				continue
			}
		}
		out = append(out, turns[i])
	}
	return out
}
