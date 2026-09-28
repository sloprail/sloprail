package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Citations across a session's records: the user pool is the end user's own
// conversation (the root record) alone; the tool_result pool is the root's and
// every sub-agent's, because a sub-agent's tool output is genuine tool output
// of the session and is recorded only in the sub-agent's own file.

// sidechainCall and sidechainAnswer are a tool call and its result inside a
// sub-agent's record — the shape toolCall/toolAnswer write, with isSidechain.
func sidechainCall(uuid, parent, id, command string) string {
	return strings.Replace(toolCall(uuid, parent, id, "Bash", command), `"isSidechain":false`, `"isSidechain":true`, 1)
}

func sidechainAnswer(uuid, parent, id, body string) string {
	return strings.Replace(toolAnswer(uuid, parent, id, body), `"isSidechain":false`, `"isSidechain":true`, 1)
}

// dispatchedSession writes a root that dispatched one sub-agent, whose record
// holds the dispatch prompt and a Bash call printing out. Returns both paths.
func dispatchedSession(t *testing.T, p *project, rootLines []string, out string) (string, string) {
	t.Helper()
	rootPath := p.write("the-session", append([]string{userMsg("u1", "research the retry policy")}, rootLines...)...)
	sub := p.writeSubagent("the-session", "a1b2",
		`{"type":"user","uuid":"s0","parentUuid":null,"isSidechain":true,"agentId":"a1b2","message":{"role":"user","content":"DISPATCHMARKER: read the retry config"}}`,
		sidechainCall("s1", "s0", "toolu_sub", "cat retry.yaml"),
		sidechainAnswer("s2", "s1", "toolu_sub", out),
	)
	p.writeSubagentMeta("the-session", "a1b2", SubagentMeta{ToolUseID: "toolu_dispatch"})
	return rootPath, sub
}

func TestResolveCitationToolResultInASubagentRecord(t *testing.T) {
	p := newProject(t)
	rootPath, sub := dispatchedSession(t, p, nil, "retries: 5 with SUBOUTPUTMARKER backoff")

	// Named by the root (the hook's transcript_path, the session id's record)
	// and by the sub-agent's own record alike, it resolves in the sub-agent's.
	for name, from := range map[string]string{"from the root": rootPath, "from the sub-agent": sub} {
		t.Run(name, func(t *testing.T) {
			got, err := ResolveCitation(from, toolReq("SUBOUTPUTMARKER backoff"))
			require.NoError(t, err)
			assert.Equal(t, sub, got.Path, "the citation points into the record it resolved in")
			assert.Equal(t, 3, got.Line)
			assert.Equal(t, []SourceType{SourceToolResult}, got.SourceTypes)
			assert.Equal(t, "retries: 5 with SUBOUTPUTMARKER backoff", got.Message)
			assert.Equal(t, "Bash: cat retry.yaml", got.Call, "the call is found in the sub-agent's own record")
		})
	}
}

func TestResolveCitationToolResultInANestedSubagentRecord(t *testing.T) {
	p := newProject(t)
	rootPath, sub := dispatchedSession(t, p, nil, "unrelated")
	nestedDir := filepath.Join(strings.TrimSuffix(sub, ".jsonl"), SubagentDir)
	require.NoError(t, os.MkdirAll(nestedDir, 0o755))
	nested := filepath.Join(nestedDir, "agent-c3d4.jsonl")
	require.NoError(t, os.WriteFile(nested, []byte(
		`{"type":"user","uuid":"n0","parentUuid":null,"isSidechain":true,"message":{"role":"user","content":"go deeper"}}`+"\n"+
			sidechainCall("n1", "n0", "toolu_nested", "ls")+"\n"+
			sidechainAnswer("n2", "n1", "toolu_nested", "NESTEDMARKER.md")+"\n"), 0o644))

	got, err := ResolveCitation(rootPath, toolReq("NESTEDMARKER"))
	require.NoError(t, err)
	assert.Equal(t, nested, got.Path)

	got, err = ResolveCitation(nested, toolReq("NESTEDMARKER"))
	require.NoError(t, err, "a nested sub-agent's record climbs to the root too")
	assert.Equal(t, nested, got.Path)
}

func TestResolveCitationUserPoolNeverReadsASubagent(t *testing.T) {
	p := newProject(t)
	rootPath, sub := dispatchedSession(t, p, nil, "SUBOUTPUTMARKER")

	// The dispatch prompt is the parent agent's words, whichever record names
	// the session.
	for _, from := range []string{rootPath, sub} {
		_, err := ResolveCitation(from, userReq("DISPATCHMARKER"))
		assert.Error(t, err, "a sub-agent's dispatch prompt is not the end user's words")
		_, err = ResolveCitation(from, userReq("SUBOUTPUTMARKER"))
		assert.Error(t, err, "a sub-agent's tool output is not the end user's words")
	}

	// The end user's own words still resolve — in the root, from either record.
	for _, from := range []string{rootPath, sub} {
		got, err := ResolveCitation(from, userReq("research the retry policy"))
		require.NoError(t, err)
		assert.Equal(t, rootPath, got.Path)
		assert.Equal(t, 1, got.Line)
	}
}

func TestResolveCitationOrphanSubagentRefusesTheUserPool(t *testing.T) {
	p := newProject(t)
	// A sub-agent record whose root is not where the layout puts it.
	sub := p.writeSubagent("gone-session", "a1b2",
		`{"type":"user","uuid":"s0","parentUuid":null,"isSidechain":true,"message":{"role":"user","content":"ORPHANPROMPT"}}`,
		sidechainCall("s1", "s0", "toolu_sub", "echo hi"),
		sidechainAnswer("s2", "s1", "toolu_sub", "ORPHANOUTPUT"),
	)

	_, err := ResolveCitation(sub, userReq("ORPHANPROMPT"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sub-agent")

	got, err := ResolveCitation(sub, toolReq("ORPHANOUTPUT"))
	require.NoError(t, err, "its own tool output is still its session's")
	assert.Equal(t, sub, got.Path)
}

// The root and a sub-agent printed the same thing — both read the same file.
// Each resolves the quote in its OWN record first: the two entries are
// identical, so no extension of the quote could tell them apart, and a root
// that could cite its own output before it dispatched anything must still be
// able to after. Only a quote the caller's own record does not hold is searched
// for across the session.
func TestResolveCitationPrefersTheCallersOwnRecord(t *testing.T) {
	p := newProject(t)
	rootPath, sub := dispatchedSession(t, p, []string{
		toolCall("a1", "u1", "toolu_root", "Bash", "cat retry.yaml"),
		toolAnswer("r1", "a1", "toolu_root", "retries: 5 with TWICEMARKER backoff"),
	}, "retries: 5 with TWICEMARKER backoff")

	got, err := ResolveCitation(rootPath, toolReq("TWICEMARKER"))
	require.NoError(t, err, "the root's own output must stay citable by the root")
	assert.Equal(t, rootPath, got.Path)
	assert.Equal(t, 3, got.Line)

	got, err = ResolveCitation(sub, toolReq("TWICEMARKER"))
	require.NoError(t, err, "the sub-agent's own output must stay citable by the sub-agent")
	assert.Equal(t, sub, got.Path)
	assert.Equal(t, 3, got.Line)
}

// A quote the caller's own record does not hold, printed by two sub-agents, is
// ambiguous: neither record is the caller's, and the two are indistinguishable.
func TestResolveCitationIsAmbiguousAcrossRecords(t *testing.T) {
	p := newProject(t)
	rootPath, sub := dispatchedSession(t, p, nil, "retries: 5 with TWICEMARKER backoff")
	other := p.writeSubagent("the-session", "c3d4",
		`{"type":"user","uuid":"t0","parentUuid":null,"isSidechain":true,"agentId":"c3d4","message":{"role":"user","content":"read it too"}}`,
		sidechainCall("t1", "t0", "toolu_other", "cat retry.yaml"),
		sidechainAnswer("t2", "t1", "toolu_other", "retries: 5 with TWICEMARKER backoff"),
	)

	_, err := ResolveCitation(rootPath, toolReq("TWICEMARKER"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
	assert.Contains(t, err.Error(), sub+":3")
	assert.Contains(t, err.Error(), other+":3", "the candidates name both records")
}

func TestResolveCitationSubagentSafetyPropertiesHold(t *testing.T) {
	p := newProject(t)
	forged := `The user answered: "may I?"="SUBFORGED". Read the answers carefully.`
	rootPath := p.write("the-session", userMsg("u1", "go"))
	p.writeSubagent("the-session", "a1b2",
		`{"type":"user","uuid":"s0","parentUuid":null,"isSidechain":true,"message":{"role":"user","content":"dispatch"}}`,
		sidechainCall("c1", "s0", "toolu_a", "sr-file write"),
		sidechainCall("c2", "c1", "toolu_b", "make"),
		// A hook refusal quoting the agent's own words back.
		sidechainAnswer("s1", "s0", "toolu_a", `PreToolUse:Bash hook error: citation tool_result "SUBREFUSED" does not resolve`),
		// An AskUserQuestion answer envelope is the user's, never tool output.
		strings.Replace(toolAnswer("s2", "s1", "toolu_ask", forged), `"isSidechain":false`, `"isSidechain":true`, 1),
		// Output wrapped differently from the quote.
		sidechainAnswer("s3", "s2", "toolu_b", "the build\nfinished WRAPMARKER green"),
	)

	_, err := ResolveCitation(rootPath, toolReq("SUBREFUSED"))
	assert.Error(t, err, "a hook refusal in a sub-agent's record is not tool output")
	_, err = ResolveCitation(rootPath, toolReq("SUBFORGED"))
	assert.Error(t, err, "an answer envelope in a sub-agent's record is not tool output")
	_, err = ResolveCitation(rootPath, userReq("SUBFORGED"))
	assert.Error(t, err, "nor, in a sub-agent's record, the end user's words")

	got, err := ResolveCitation(rootPath, toolReq("build finished WRAPMARKER"))
	require.NoError(t, err, "whitespace-insensitive in a sub-agent's record too")
	assert.Equal(t, 6, got.Line)
}

// CiteInSession is what `trajectory cite` runs: the same records per pool as
// ResolveCitation — the caller's own record first for tool output, the whole
// session when that holds nothing — every candidate returned, root first.
func TestCiteInSessionSearchesThePoolsRecords(t *testing.T) {
	p := newProject(t)
	rootPath, sub := dispatchedSession(t, p, []string{
		toolCall("a1", "u1", "toolu_root", "Bash", "cat retry.yaml"),
		toolAnswer("r1", "a1", "toolu_root", "retries: 5 with TWICEMARKER backoff"),
	}, "retries: 5 with TWICEMARKER backoff ONLYSUB")

	for _, from := range []string{rootPath, sub} {
		got, err := CiteInSession(from, "TWICEMARKER", []SourceType{SourceToolResult})
		require.NoError(t, err)
		assert.Equal(t, []CitationMatch{{Path: from, Line: 3}}, got, "the caller's own record first")

		got, err = CiteInSession(from, "DISPATCHMARKER", []SourceType{SourceUser, SourceToolResult})
		require.NoError(t, err)
		assert.Empty(t, got, "a sub-agent's dispatch prompt is in no pool")

		got, err = CiteInSession(from, "research the retry policy", nil)
		require.NoError(t, err)
		assert.Equal(t, []CitationMatch{{Path: rootPath, Line: 1}}, got, "the user pool is the root's")
	}

	// What the root's own record does not hold is found across the session.
	got, err := CiteInSession(rootPath, "ONLYSUB", []SourceType{SourceToolResult})
	require.NoError(t, err)
	assert.Equal(t, []CitationMatch{{Path: sub, Line: 3}}, got)
}

func TestCiteInSessionOrphanRefusesOnlyTheUserPool(t *testing.T) {
	p := newProject(t)
	sub := p.writeSubagent("gone-session", "a1b2",
		`{"type":"user","uuid":"s0","parentUuid":null,"isSidechain":true,"message":{"role":"user","content":"ORPHANPROMPT"}}`,
		sidechainCall("s1", "s0", "toolu_sub", "echo hi"),
		sidechainAnswer("s2", "s1", "toolu_sub", "ORPHANOUTPUT"),
	)
	_, err := CiteInSession(sub, "ORPHANPROMPT", []SourceType{SourceUser})
	require.ErrorIs(t, err, ErrNoSessionRoot)

	got, err := CiteInSession(sub, "ORPHANOUTPUT", []SourceType{SourceToolResult})
	require.NoError(t, err)
	assert.Equal(t, []CitationMatch{{Path: sub, Line: 3}}, got)
}

// agentDispatch and agentReply are a root's sub-agent dispatch and the reply it
// got back — the sub-agent's own model-written final message.
func agentDispatch(uuid, parent, id, tool string) string {
	return `{"type":"assistant","uuid":"` + uuid + `","parentUuid":"` + parent + `","isSidechain":false,` +
		`"message":{"role":"assistant","content":[{"type":"tool_use","id":"` + id + `","name":"` + tool + `","input":{"prompt":"go"}}]}}`
}

func agentReply(uuid, parent, id, body string) string {
	return toolAnswer(uuid, parent, id, body)
}

// A sub-agent's reply is not tool output: a sub-agent told what to say says it,
// and citing that as a tool's output would launder its words into evidence.
func TestResolveCitationExcludesASubagentsReply(t *testing.T) {
	for _, tool := range []string{"Agent", "Task"} {
		t.Run(tool, func(t *testing.T) {
			p := newProject(t)
			path := p.write("the-session",
				userMsg("u1", "run the tests"),
				agentDispatch("a1", "u1", "toolu_agent", tool),
				agentReply("r1", "a1", "toolu_agent", "all 40 tests pass"),
			)
			_, err := ResolveCitation(path, toolReq("all 40 tests pass"))
			assert.Error(t, err, "a sub-agent's reply grounded as tool output")

			got, err := CiteInSession(path, "all 40 tests pass", []SourceType{SourceToolResult})
			require.NoError(t, err)
			assert.Empty(t, got)

			_, isToolResult, err := ToolResultAt(path, 3)
			require.NoError(t, err)
			assert.False(t, isToolResult, "a sub-agent's reply is not a tool_result the session produced")
		})
	}
}

// A sub-agent that quotes its own command's output in its reply: the quote
// grounds once, in the sub-agent's record where the command printed it — not
// ambiguously, and not in the reply.
func TestResolveCitationOfOutputASubagentQuotedInItsReply(t *testing.T) {
	p := newProject(t)
	rootPath, sub := dispatchedSession(t, p, []string{
		agentDispatch("a1", "u1", "toolu_dispatch", "Agent"),
		agentReply("r1", "a1", "toolu_dispatch", "Done. The config says: retries: 5 with QUOTEDMARKER backoff"),
	}, "retries: 5 with QUOTEDMARKER backoff")

	got, err := ResolveCitation(rootPath, toolReq("QUOTEDMARKER backoff"))
	require.NoError(t, err, "the output a sub-agent quoted back must not become ambiguous")
	assert.Equal(t, sub, got.Path)
	assert.Equal(t, 3, got.Line)
	assert.Equal(t, "Bash: cat retry.yaml", got.Call)
}

// A sub-agent never sees the user's messages. Quoting its dispatch prompt as the
// user is its commonest miss, and the refusal says so — and how --cite:user
// works for it.
func TestUnresolvedUserCitationTellsASubagentWhy(t *testing.T) {
	p := newProject(t)
	rootPath, sub := dispatchedSession(t, p, nil, "out")

	_, err := ResolveCitation(sub, userReq("DISPATCHMARKER: read the retry config"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "That quote is from your dispatch prompt, written by the parent agent.")
	assert.Contains(t, err.Error(), SubagentUserAdvice)

	_, err = ResolveCitation(sub, userReq("never said by anyone"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), SubagentUserAdvice, "a sub-agent is told how --cite:user works for it")
	assert.NotContains(t, err.Error(), "dispatch prompt")

	// From the root's record the caller is not known: the prompt is named, the
	// caller is not assumed.
	_, err = ResolveCitation(rootPath, userReq("DISPATCHMARKER"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "from a sub-agent's dispatch prompt, written by the parent agent, not by the user")
	_, err = ResolveCitation(rootPath, userReq("never said by anyone"))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "sub-agent", "the root is told nothing about sub-agents")

	// Only the user pool: a tool_result miss is not about the user's words.
	_, err = ResolveCitation(sub, toolReq("DISPATCHMARKER"))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "sub-agent")
}

// A record a harness filed at <session>/subagents/agent-<id>.jsonl is a
// sub-agent's even when nothing inside it says so — no meta.json beside it (a
// harness that stopped writing one, a record not fully written yet) and no
// isSidechain on its first record. Read as a root, its first "user" message —
// the parent agent's dispatch — would ground as the end user's words.
func TestResolveCitationSubagentByLayoutAlone(t *testing.T) {
	p := newProject(t)
	rootPath := p.write("the-session", userMsg("u1", "research the retry policy"))
	sub := p.writeSubagent("the-session", "e5f6",
		`{"type":"user","uuid":"s0","parentUuid":null,"message":{"role":"user","content":"DISPATCHMARKER: read the retry config"}}`,
		toolCall("s1", "s0", "toolu_sub", "Bash", "cat retry.yaml"),
		toolAnswer("s2", "s1", "toolu_sub", "LAYOUTOUTPUT retries: 5"),
	)
	require.False(t, IsSubagentTranscript(sub), "the fixture must carry no content mark, or this tests nothing")

	_, err := ResolveCitation(sub, userReq("DISPATCHMARKER"))
	var rerr *ResolutionError
	require.ErrorAs(t, err, &rerr, "a dispatch prompt grounded as the user's words")

	got, err := ResolveCitation(sub, userReq("research the retry policy"))
	require.NoError(t, err, "the user's words resolve in the root the layout names")
	assert.Equal(t, rootPath, got.Path)

	got, err = ResolveCitation(sub, toolReq("LAYOUTOUTPUT"))
	require.NoError(t, err)
	assert.Equal(t, sub, got.Path)

	// Orphaned by layout: no root on disk, so the user pool cannot be searched.
	orphan := p.writeSubagent("gone-session", "e5f6",
		`{"type":"user","uuid":"s0","parentUuid":null,"message":{"role":"user","content":"ORPHANDISPATCH go"}}`,
	)
	_, err = ResolveCitation(orphan, userReq("ORPHANDISPATCH"))
	require.ErrorAs(t, err, &rerr)
	assert.Contains(t, err.Error(), "sub-agent")
}

// A caller the hook KNOWS is a sub-agent (the payload says so) whose record
// neither says so nor sits where a sub-agent's does: there is no root to climb
// to, and the user pool must be refused rather than searched in the
// sub-agent's own record.
func TestResolveSubagentCitationWithNoDerivableRoot(t *testing.T) {
	p := newProject(t)
	stray := p.write("agent-stray",
		`{"type":"user","uuid":"s0","parentUuid":null,"message":{"role":"user","content":"STRAYDISPATCH go"}}`,
		toolCall("s1", "s0", "toolu_sub", "Bash", "echo hi"),
		toolAnswer("s2", "s1", "toolu_sub", "STRAYOUTPUT hi"),
	)
	_, err := ResolveSubagentCitation(stray, userReq("STRAYDISPATCH"))
	var rerr *ResolutionError
	require.ErrorAs(t, err, &rerr, "a known sub-agent's dispatch grounded as the user's words")
	assert.Contains(t, err.Error(), "sub-agent")

	got, err := ResolveSubagentCitation(stray, toolReq("STRAYOUTPUT"))
	require.NoError(t, err, "its own tool output is still citable")
	assert.Equal(t, stray, got.Path)

	// A sub-agent whose record is where the layout puts it climbs as before.
	rootPath, sub := dispatchedSession(t, p, nil, "out")
	got, err = ResolveSubagentCitation(sub, userReq("research the retry policy"))
	require.NoError(t, err)
	assert.Equal(t, rootPath, got.Path)
}

// A tool-output quote that is in the record, but in a result the tool-output
// pool excludes, fails with a reason rather than "not there word for word".
func TestUnresolvedToolResultSaysWhyAnExcludedResultIsNot(t *testing.T) {
	p := newProject(t)
	path := p.write("the-session",
		userMsg("u1", "run the tests"),
		agentDispatch("a1", "u1", "toolu_agent", "Agent"),
		agentReply("r1", "a1", "toolu_agent", "REPLYMARKER all 40 tests pass"),
		toolAnswer("r2", "r1", "toolu_gone", "ORPHANMARKER all green"),
		namedCall("a3", "r2", "toolu_ag", "Agent", `{"prompt":"go","run_in_background":true}`),
		toolAnswer("r3", "a3", "toolu_ag", "Async agent launched successfully.\nagentId: bg42 (internal ID)"),
		namedCall("a4", "r3", "toolu_out", "TaskOutput", `{"task_id":"bg42"}`),
		toolAnswer("r4", "a4", "toolu_out", "BGREPLYMARKER done"),
		namedCall("a5", "r4", "toolu_q", "AskUserQuestion", `{"questions":[]}`),
		toolAnswer("r5", "a5", "toolu_q", `The user answered: "which?"="ANSWERMARKER five". Read the answers carefully.`),
	)
	for quote, want := range map[string]string{
		"REPLYMARKER":   "a sub-agent's reply",
		"ORPHANMARKER":  "the call that produced it is not in the record",
		"BGREPLYMARKER": "TaskOutput returned",
		"ANSWERMARKER":  "--cite:user",
	} {
		_, err := ResolveCitation(path, toolReq(quote))
		require.Error(t, err, quote)
		assert.Contains(t, err.Error(), want, quote)
	}
	// A quote that is nowhere keeps the plain message.
	_, err := ResolveCitation(path, toolReq("NOWHEREMARKER"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not there word for word")
	assert.NotContains(t, err.Error(), "reply")
}
