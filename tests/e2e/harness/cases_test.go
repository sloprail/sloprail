package harness

type renderCase struct {
	Name string
	Turn Turn
}

// renderCases is one Turn from every constructor (every shape of each), by name:
// the table TestClaudeRenderingUnchanged renders and compares with
// testdata/claude_render.golden.
func renderCases() []renderCase {
	var out []renderCase
	add := func(name string, t Turn) { out = append(out, renderCase{name, t}) }
	add("Write", Write("w1", "/p/a.txt", "line1\nline \"2\"\n"))
	add("Edit", Edit("e1", "/p/a.txt", "old\ttext", "new\\text"))
	add("Bash", Bash("b1", "echo 'hi' && ls \"x\""))
	add("WebFetch", WebFetch("wf1", "https://example.com/a", "summarise"))
	add("Say", Say("s1", "I will do #refactor now\nok"))
	add("ToolUse", ToolUse("t1", "fill_form", map[string]string{"email": "a@b.c"}))
	add("ToolUseTyped", ToolUse("t2", "Read", map[string]string{"file_path": "/x", "limit": "20"}))
	add("ToolUseJSON", ToolUseJSON("tj1", "screenshot", `{"w":3,"nested":{"a":[1,2]}}`))
	u, r := ToolUseWithResult("tr1", "screenshot", map[string]string{"url": "http://x"}, `{"image":"abc"}`)
	add("ToolUseWithResult.use", u)
	add("ToolUseWithResult.result", r)
	u, r = CallWithOutput("co1", "Bash", map[string]string{"command": "ls"}, "out text\n")
	add("CallWithOutput.use", u)
	add("CallWithOutput.result", r)
	add("AnswerQuestion", AnswerQuestion("aq1", [2]string{"Q1?", "A1"}, [2]string{"Q2?", "A2"}))
	u, r = AskUserQuestion("auq1", "Which?", "this one")
	add("AskUserQuestion.use", u)
	add("AskUserQuestion.answer", r)
	add("ToolResult", ToolResult("res1", "all green\n"))
	add("ToolResultWithText", ToolResultWithText("rwt1", "result body", "typed words"))
	add("Skill", Skill("sk1", "my-skill"))
	add("Dispatch", Dispatch("d1", "do it", "/tmp/sub.sh", ""))
	add("DispatchIsolated", Dispatch("d2", "do it", "/tmp/sub.sh", "worktree"))
	add("DispatchNoParent", DispatchNoParent("dnp1", "do it", "/tmp/sub.sh"))
	add("SayWrite", SayWrite("sw1", "#tag here", "/p/f.md", "body\n"))
	add("SayBash", SayBash("sb1", "#tag here", "echo hi"))
	add("BashBatch", BashBatch("bb1", "echo 1", "echo 2", "echo 3"))
	add("Compact", Compact("c1"))
	add("CompactNamingUnwrittenParent", CompactNamingUnwrittenParent("c2"))
	add("Background", Background("bg1", "Bash", map[string]string{"command": "sleep 1"}))
	add("BackgroundAgent", Background("bg2", "Agent", map[string]string{"prompt": "p"}))
	add("ReadLaunchedOutput", ReadLaunchedOutput("rlo1"))
	add("Commit", Commit("cm1", "msg", "Trailer: x"))
	add("CommitPaths", CommitPaths("cp1", "msg", "a.go", "b.go"))
	return out
}
