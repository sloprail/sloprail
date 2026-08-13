package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests drive the pre-tool dispatch itself, which is the only place the
// skip is actually applied. The e2e harness cannot reach it: a10n-claude-mock
// omits transcript_path from its PreToolUse payload, and without that the
// session cannot be identified, so every hook re-judges and a test driven
// through the mock would pass whether the skip worked or not.
//
// What it cannot skip, it must not silently skip either — the last case here
// pins exactly that: no session, no exemption.

// dispatch is one project set up so runSessionPreTool can be called against it,
// with everything the engine reads from the environment pointed at temp dirs.
type dispatch struct {
	t       *testing.T
	proj    string
	session string
}

// newDispatch prepares a project, an isolated data home for the session store,
// and an isolated config dir holding a transcript for the conversation.
func newDispatch(t *testing.T) *dispatch {
	t.Helper()
	// Short root: the config dir encodes the project path 1:1 into a single
	// filename component, and a long test name pushes it past the limit.
	root, err := os.MkdirTemp("", "slop-disp-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(root) })

	proj := filepath.Join(root, "proj")
	require.NoError(t, os.MkdirAll(proj, 0o755))

	cfg := filepath.Join(root, "cfg")
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))

	d := &dispatch{t: t, proj: proj, session: "conv-root-uuid"}
	d.seedTranscript(cfg)
	return d
}

// seedTranscript writes the one record a conversation's identity is read from:
// a root with a uuid and an explicitly null parent.
func (d *dispatch) seedTranscript(cfg string) {
	d.t.Helper()
	resolved := d.proj
	if r, err := filepath.EvalSymlinks(d.proj); err == nil {
		resolved = r
	}
	dir := filepath.Join(cfg, "projects", nonAlnum.ReplaceAllString(resolved, "-"))
	require.NoError(d.t, os.MkdirAll(dir, 0o755))
	line := fmt.Sprintf(`{"type":"user","uuid":%q,"parentUuid":null,"cwd":%q,"message":{"role":"user","content":"go"}}`+"\n",
		d.session, d.proj)
	require.NoError(d.t, os.WriteFile(d.transcript(), []byte(line), 0o644))
}

func (d *dispatch) transcript() string {
	resolved := d.proj
	if r, err := filepath.EvalSymlinks(d.proj); err == nil {
		resolved = r
	}
	return filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects",
		nonAlnum.ReplaceAllString(resolved, "-"), d.session+".jsonl")
}

// guardrail declares a rule bound to file creations and changes whose hook
// appends a line to a log every time it is asked, then exits with code.
//
// A counter rather than a fixed answer: what is under test is which hooks the
// engine decides to run at all, which is invisible when every run looks alike.
func (d *dispatch) guardrail(name string, exitCode int) {
	d.t.Helper()
	dir := filepath.Join(d.proj, ".sloprail", "guardrails", name)
	require.NoError(d.t, os.MkdirAll(dir, 0o755))

	const decl = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./count.sh
  PreFileUpdate:
    - hooks:
        - type: command
          command: ./count.sh
---

# Records every time it is asked
`
	require.NoError(d.t, os.WriteFile(filepath.Join(dir, "GUARDRAIL.md"), []byte(decl), 0o644))

	script := fmt.Sprintf("#!/bin/sh\ncat >/dev/null\necho ran >> ./ran.log\necho 'the rule says no' >&2\nexit %d\n", exitCode)
	require.NoError(d.t, os.WriteFile(filepath.Join(dir, "count.sh"), []byte(script), 0o755))
}

// runs counts how many times a guardrail's hook was asked.
func (d *dispatch) runs(name string) int {
	d.t.Helper()
	body, err := os.ReadFile(filepath.Join(d.proj, ".sloprail", "guardrails", name, "ran.log"))
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(d.t, err)
	return strings.Count(strings.TrimSpace(string(body)), "ran")
}

// write drives one pre-tool dispatch for a Write of content at path, then
// performs the write — which is what a harness does once the hook permits it,
// and what turns the next offering of the same path into a change.
func (d *dispatch) write(path, content string) string {
	d.t.Helper()
	payload, err := json.Marshal(HookPayload{
		TranscriptPath: d.transcript(),
		Cwd:            d.proj,
		ToolName:       "Write",
		ToolInput:      json.RawMessage(fmt.Sprintf(`{"file_path":%q,"content":%q}`, path, content)),
	})
	require.NoError(d.t, err)

	var out, errOut bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetIn(bytes.NewReader(payload))
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	require.NoError(d.t, runSessionPreTool(cmd, nil))

	if strings.Contains(out.String(), `"deny"`) {
		// Refused: the write does not land, so the file keeps whatever it had.
		return out.String()
	}
	full := filepath.Join(d.proj, path)
	require.NoError(d.t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(d.t, os.WriteFile(full, []byte(content), 0o644))
	return out.String()
}

func TestPreTool_SameContentIsJudgedOnce(t *testing.T) {
	d := newDispatch(t)
	d.guardrail("counter", 0)

	d.write("notes.md", "hello")
	d.write("notes.md", "hello")

	assert.Equal(t, 1, d.runs("counter"),
		"the same content offered twice must reach the hook once — the second offering is settled work")
}

func TestPreTool_ChangedContentIsJudgedAgain(t *testing.T) {
	d := newDispatch(t)
	d.guardrail("counter", 0)

	d.write("notes.md", "hello")
	d.write("notes.md", "hello, but different")

	assert.Equal(t, 2, d.runs("counter"),
		"an edited file must not inherit its predecessor's verdict")
}

func TestPreTool_RefusalReFiresOnUnchangedContent(t *testing.T) {
	// Without the passing half of the exemption the second offering would be
	// skipped, the violation would go quiet, and the write would sail through.
	d := newDispatch(t)
	d.guardrail("counter", 1)

	first := d.write("notes.md", "hello")
	second := d.write("notes.md", "hello")

	assert.Equal(t, 2, d.runs("counter"),
		"an unfixed violation must be put back in front of the rule every cycle")
	assert.Contains(t, first, "the rule says no")
	assert.Contains(t, second, "the rule says no", "the refusal must still reach the agent the second time")
}

func TestPreTool_EachGuardrailJudgesForItself(t *testing.T) {
	// A pooled per-file verdict would exempt every already-judged file the
	// moment a guardrail is added. Two rules, same event: each must judge once.
	d := newDispatch(t)
	d.guardrail("first", 0)
	d.guardrail("second", 0)

	d.write("notes.md", "hello")
	d.write("notes.md", "hello")

	assert.Equal(t, 1, d.runs("first"))
	assert.Equal(t, 1, d.runs("second"))
}

func TestPreTool_GuardrailAddedLaterJudgesWhatIsAlreadySettled(t *testing.T) {
	// The case a per-file verdict gets exactly backwards: a rule declared after
	// a file was judged has never seen it, and must judge it despite the
	// content being unchanged since another rule passed it.
	d := newDispatch(t)
	d.guardrail("first", 0)

	d.write("notes.md", "hello")
	require.Equal(t, 1, d.runs("first"))

	d.guardrail("second", 0)
	d.write("notes.md", "hello")

	assert.Equal(t, 1, d.runs("first"), "the rule that already passed this content is spared")
	assert.Equal(t, 1, d.runs("second"), "the rule that has never seen it is not")
}

func TestPreTool_WithoutASessionNothingIsExempt(t *testing.T) {
	// No transcript path on the payload means the conversation cannot be
	// identified, so there is no record to consult. The engine must re-judge
	// rather than exempt: losing the record costs work, never enforcement.
	d := newDispatch(t)
	d.guardrail("counter", 0)

	for range 2 {
		payload, err := json.Marshal(HookPayload{
			Cwd:       d.proj,
			ToolName:  "Write",
			ToolInput: json.RawMessage(`{"file_path":"notes.md","content":"hello"}`),
		})
		require.NoError(t, err)

		cmd := &cobra.Command{}
		cmd.SetIn(bytes.NewReader(payload))
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		require.NoError(t, runSessionPreTool(cmd, nil))
	}

	assert.Equal(t, 2, d.runs("counter"),
		"with no session record every offering must be judged afresh")
}
