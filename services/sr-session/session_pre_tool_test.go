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

	"github.com/sloprail/sloprail/internal/fingerprint"
)

// These tests drive the pre-tool dispatch itself, which is where the skip is
// applied.
//
// They are not the only reach into it. An earlier version of this file claimed
// the e2e harness could not get here at all, because a10n-claude-mock omits
// transcript_path from its PreToolUse payload — the omission is real, the
// conclusion was not, and an e2e was deleted over it. The record is located by
// the harness's own naming instead, and tests/e2e/pre_tool/013 drives all of
// this through a real session.
//
// What these add is what a session cannot stage: a store inspected directly
// (a refusal that was recorded rather than merely obeyed), a hook that rewrites
// the file mid-dispatch, and a payload deliberately stripped of its record.

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
	dir := filepath.Join(cfg, "projects", encodePath(resolved))
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
		encodePath(resolved), d.session+".jsonl")
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

// verdictOf makes a guardrail's answer depend on a file the test controls, so
// one rule can refuse in one cycle and permit in the next while the CONTENT it
// is judging stays identical. That separation is what the refusal-retained
// property needs: a refusal that is never recorded is invisible for as long as
// the rule keeps refusing.
func (d *dispatch) switchable(name string) {
	d.t.Helper()
	dir := filepath.Join(d.proj, ".sloprail", "guardrails", name)
	require.NoError(d.t, os.MkdirAll(dir, 0o755))

	const decl = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./judge.sh
  PreFileUpdate:
    - hooks:
        - type: command
          command: ./judge.sh
---

# Answers whatever ./verdict says
`
	require.NoError(d.t, os.WriteFile(filepath.Join(dir, "GUARDRAIL.md"), []byte(decl), 0o644))

	script := "#!/bin/sh\ncat >/dev/null\necho ran >> ./ran.log\n" +
		"if [ \"$(cat ./verdict 2>/dev/null)\" = refuse ]; then echo 'the rule says no' >&2; exit 1; fi\nexit 0\n"
	require.NoError(d.t, os.WriteFile(filepath.Join(dir, "judge.sh"), []byte(script), 0o755))
}

// setVerdict tells a switchable guardrail what to answer next.
func (d *dispatch) setVerdict(name, answer string) {
	d.t.Helper()
	require.NoError(d.t, os.WriteFile(
		filepath.Join(d.proj, ".sloprail", "guardrails", name, "verdict"), []byte(answer), 0o644))
}

func TestPreTool_RefusalIsRecordedNotDropped(t *testing.T) {
	// FINDING 2. refusal_is_retained, pinned at the DISPATCHER rather than at the
	// store. Recording only passes survives every other test here, because a
	// refusal denies immediately and so a cycle that dropped the row looks
	// exactly like one that wrote it.
	//
	// What separates them is what the row is worth a cycle LATER. So the rule
	// refuses once, then permits — and because the content never changed, the
	// pass is recorded under the same fingerprint the refusal was. Offer the
	// content a third time and the question becomes: was there a row to
	// overwrite, or did the pass land on an empty slot?
	//
	// Both answers skip on cycle 3, which is why the assertion is on cycle 2:
	// with the refusal recorded, cycle 2 finds a row that FAILS the passing half
	// and re-judges. Without it, cycle 2 finds nothing — which is also a
	// re-judge. So the pin has to be the row itself.
	d := newDispatch(t)
	d.switchable("counter")
	d.setVerdict("counter", "refuse")

	out := d.write("notes.md", "hello")
	require.Contains(t, out, "the rule says no", "cycle 1 refuses, so the write does not land")
	require.Equal(t, 1, d.runs("counter"))

	// The refusal must be in the record now — under the fingerprint of the
	// content that was judged, marked as NOT passing. Read it directly: this is
	// the fact the dispatcher is responsible for, and every behavioural proxy
	// for it is indistinguishable from the mutant.
	id, err := stableID(HookPayload{TranscriptPath: d.transcript(), Cwd: d.proj})
	require.NoError(t, err)
	rev, err := openRevalidation(id, d.proj)
	require.NoError(t, err)
	defer rev.Close()

	v, found, err := rev.store.FileCheck("notes.md", "counter")
	require.NoError(t, err)
	require.True(t, found, "the refusal must be written, not dropped — with no row the next pass lands on an empty slot")
	assert.False(t, v.Passed, "and it must be written AS a refusal")
	assert.Equal(t, fingerprint.Of([]byte("hello")), v.Fingerprint,
		"keyed on the content that was judged, so it stops being a licence the moment that content changes")
}

func TestPreTool_ARecordedRefusalIsWhatTheNextPassReplaces(t *testing.T) {
	// The consequence of the row above, driven through the dispatcher: the
	// refusal stands until the SAME guardrail permits the SAME content, and only
	// then is the file exempt. Content identical in all three cycles, so the only
	// thing that moves is the verdict.
	d := newDispatch(t)
	d.switchable("counter")
	d.setVerdict("counter", "refuse")

	first := d.write("notes.md", "hello")
	require.Contains(t, first, "the rule says no")
	require.Equal(t, 1, d.runs("counter"))

	// Still refusing, and still unchanged content: the stored refusal must not
	// exempt it.
	second := d.write("notes.md", "hello")
	require.Contains(t, second, "the rule says no", "an unfixed violation resurfaces every cycle")
	require.Equal(t, 2, d.runs("counter"))

	// The rule relents. Same bytes, so the pass overwrites the refusal.
	d.setVerdict("counter", "permit")
	third := d.write("notes.md", "hello")
	require.NotContains(t, third, "deny")
	require.Equal(t, 3, d.runs("counter"))

	// And now, and only now, it is settled.
	d.write("notes.md", "hello")
	assert.Equal(t, 3, d.runs("counter"), "once permitted, the same content is left alone")
}

// meddler declares a rule whose hook REWRITES the file the event is about
// before permitting it. A guardrail is an arbitrary shell script, so this is
// something a real one can do — a formatter that fixes what it objects to
// rather than refusing it is the ordinary case.
func (d *dispatch) meddler(name, path, content string) {
	d.t.Helper()
	dir := filepath.Join(d.proj, ".sloprail", "guardrails", name)
	require.NoError(d.t, os.MkdirAll(dir, 0o755))

	const decl = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./fix.sh
  PreFileUpdate:
    - hooks:
        - type: command
          command: ./fix.sh
---

# Rewrites the file, then permits
`
	require.NoError(d.t, os.WriteFile(filepath.Join(dir, "GUARDRAIL.md"), []byte(decl), 0o644))

	script := fmt.Sprintf("#!/bin/sh\ncat >/dev/null\necho ran >> ./ran.log\nprintf %%s %q > %q\nexit 0\n",
		content, filepath.Join(d.proj, path))
	require.NoError(d.t, os.WriteFile(filepath.Join(dir, "fix.sh"), []byte(script), 0o755))
}

func TestPreTool_EveryGuardrailJudgesTheOneSubjectTheEventNamed(t *testing.T) {
	// M10, and the honest version of it.
	//
	// The claim was "subject once per event, skip asked per guardrail". Half of
	// that is a real property and is pinned here; the other half turned out not
	// to be a behavioural claim at all once Finding 1 was fixed. Both halves are
	// worth stating, because the reason it is safe is the interesting part.
	//
	// The REAL property, asserted below: every guardrail bound to one event is
	// recorded against the SAME subject — the content that event is about — even
	// when an earlier rule's hook has rewritten the file underneath them. Rows
	// that disagreed would key the next cycle's exemptions on content that was
	// never the pending action.
	//
	// Why moving the resolution into the guardrail loop is now merely wasteful
	// rather than wrong: after Finding 1, the only kind this hook point can
	// produce a subject for is PreFileCreate, whose subject is the pending bytes
	// ON THE EVENT. It reads no disk, so re-resolving it cannot observe anything
	// an earlier hook did. TestRevalidation_PreSubjectDoesNotMoveWhenTheDiskDoes
	// pins exactly that, and it is what makes the two placements equivalent —
	// which is a fact about Subject, not about this loop, so that is where it is
	// pinned.
	//
	// The first rule here rewrites the file and permits; all three must still be
	// recorded against what the event named.
	d := newDispatch(t)
	d.meddler("a-meddler", "notes.md", "REWRITTEN BY THE HOOK")
	d.guardrail("b-quiet", 0)
	d.guardrail("c-quiet", 0)

	d.write("notes.md", "hello")

	id, err := stableID(HookPayload{TranscriptPath: d.transcript(), Cwd: d.proj})
	require.NoError(t, err)
	rev, err := openRevalidation(id, d.proj)
	require.NoError(t, err)
	defer rev.Close()

	want := fingerprint.Of([]byte("hello"))
	require.NotEqual(t, want, fingerprint.Of([]byte("REWRITTEN BY THE HOOK")),
		"the meddler must actually change what a disk-reading subject would be, or this test proves nothing")

	for _, n := range []string{"a-meddler", "b-quiet", "c-quiet"} {
		v, found, err := rev.store.FileCheck("notes.md", n)
		require.NoError(t, err)
		require.Truef(t, found, "guardrail %q has its own row", n)
		assert.Equalf(t, want, v.Fingerprint,
			"guardrail %q must be recorded against the event's one subject, not against whatever an earlier hook left on disk", n)
	}
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
