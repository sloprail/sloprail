package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/fingerprint"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// The revalidation half of the Post dispatch: what a cycle may skip, what it
// remembers, and what a remembered refusal does on the cycle after.
//
// These go through runPostDispatch rather than through revalidation directly,
// because the claims are about the DISPATCHER asking — a store that records
// correctly while nothing calls it is exactly the state this path was in.

// postSession runs a Post dispatch the way the hook point does, against a
// session whose store the test can read afterwards.
//
// The payload carries a transcript path, because openRevalidation resolves the
// conversation's identity from the record rather than from the reported id.
type postSession struct {
	t     *testing.T
	proj  string
	p     HookPayload
	store sessionstate.Store
}

func newPostSession(t *testing.T, proj string) *postSession {
	t.Helper()

	// The project root, handed to hook scripts explicitly. A hook's working
	// directory is its guardrail's folder, so a script reaching the tree by
	// counting "../.." depends on how deep a guardrail happens to sit — and on
	// macOS it also has to survive /var being a symlink to /private/var, which
	// a relative walk does not.
	t.Setenv("SLOPRAIL_TEST_PROJECT", proj)

	// A real transcript, so the session resolves the way it does in a session.
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	dir := filepath.Join(cfg, "projects", encodeWorkspace(proj))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	tp := filepath.Join(dir, "sess.jsonl")
	require.NoError(t, os.WriteFile(tp,
		[]byte(`{"type":"user","uuid":"root-1","parentUuid":null,"cwd":"`+proj+`","message":{"role":"user","content":"go"}}`+"\n"), 0o644))

	// The engine's own data home, isolated, so the session database this test
	// reads is the one the dispatch wrote.
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	p := HookPayload{Cwd: proj, TranscriptPath: tp, SessionID: "sess"}
	store := openStore(t)
	return &postSession{t: t, proj: proj, p: p, store: store}
}

// run dispatches one cycle and returns what was written to stdout and stderr.
// stdout is where a block travels.
func (s *postSession) run() (stdout, stderr string) {
	s.t.Helper()
	var out, errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	runPostDispatch(cmd, s.store, s.p)
	return out.String(), errBuf.String()
}

// check reads the verdict the dispatch stored for a (path, guardrail) pair.
func (s *postSession) check(path, guardrail string) (sessionstate.Verdict, bool) {
	s.t.Helper()
	id, err := stableID(s.p)
	require.NoError(s.t, err)
	rev, err := openRevalidation(id, s.p.Cwd)
	require.NoError(s.t, err)
	defer rev.Close()
	v, found, err := rev.store.FileCheck(path, guardrail)
	require.NoError(s.t, err)
	return v, found
}

const recordAndPass = `#!/bin/sh
cat >> "$SLOPRAIL_TEST_LEDGER/events.jsonl"
printf '\n' >> "$SLOPRAIL_TEST_LEDGER/events.jsonl"
`

// TestPostDispatch_RefusalIsRecordedNotDropped is refusal_is_retained, asserted
// on the STORED ROW rather than on anything the cycle printed.
//
// A refusal that is forgotten stops being enforced. The cost does not show up
// in the cycle that refused — it blocks either way — it shows up on the next
// one: with no row the content reads as unjudged rather than refused, and the
// first thing to record a pass for it is believed.
//
// The row is what the reviewer caught the pre-tool path failing to pin at the
// dispatch layer, so this reads the database directly.
func TestPostDispatch_RefusalIsRecordedNotDropped(t *testing.T) {
	proj := initRepo(t)
	ldir := ledgerDir(t)
	guardrailDir(t, proj, "refuser", `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./refuse.sh
---

# Refuses every created file
`, map[string]string{"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho 'not allowed' >&2\nexit 1\n"})
	_ = ldir

	s := newPostSession(t, proj)
	baselineAt(t, s.store, proj)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "bad.md"), []byte("content"), 0o644))

	s.run()

	v, found := s.check("bad.md", "refuser")
	require.True(t, found, "the refusal was not recorded — a refusal that is forgotten stops being enforced")
	assert.False(t, v.Passed, "the stored verdict must say it failed")
	assert.Equal(t, fingerprint.Of([]byte("content")), v.Fingerprint,
		"the verdict is keyed on the content it was reached about")
}

// TestPostDispatch_PassIsRecordedAndSkipsNextCycle is exemption_needs_pass,
// both halves.
//
// The first cycle judges and records a pass; the second, with the content
// unchanged, must not ask again. Re-judging is not merely waste — a judge hook
// is a model call rather than a function, so a second look can return a
// different answer and block the agent over work it already settled.
func TestPostDispatch_PassIsRecordedAndSkipsNextCycle(t *testing.T) {
	proj := initRepo(t)
	ldir := ledgerDir(t)
	guardrailDir(t, proj, "permits", `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./record.sh
---

# Permits, and records that it ran
`, map[string]string{"record.sh": recordAndPass})

	s := newPostSession(t, proj)
	baselineAt(t, s.store, proj)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "ok.md"), []byte("fine"), 0o644))

	s.run()
	require.Len(t, kindsSeen(t, ldir), 1, "the hook must run the first time")

	v, found := s.check("ok.md", "permits")
	require.True(t, found)
	require.True(t, v.Passed)

	// Second cycle, same content. The hook must not be asked again.
	s.run()
	assert.Len(t, kindsSeen(t, ldir), 1,
		"the hook ran again for content it had already passed — the exemption is not being applied")
}

// TestPostDispatch_EditedFileIsJudgedAgain: the content half of
// exemption_needs_pass.
//
// A file edited after being passed must NOT inherit its predecessor's verdict.
// Without the fingerprint match, one pass would license every later version of
// the file — which is the whole of the rule going quiet after the first cycle.
func TestPostDispatch_EditedFileIsJudgedAgain(t *testing.T) {
	proj := initRepo(t)
	ldir := ledgerDir(t)
	guardrailDir(t, proj, "permits", `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./record.sh
  PostFileUpdate:
    - hooks:
        - type: command
          command: ./record.sh
---

# Permits, and records that it ran
`, map[string]string{"record.sh": recordAndPass})

	s := newPostSession(t, proj)
	baselineAt(t, s.store, proj)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "f.md"), []byte("first"), 0o644))
	s.run()
	require.Len(t, kindsSeen(t, ldir), 1)

	// Same path, different bytes.
	require.NoError(t, os.WriteFile(filepath.Join(proj, "f.md"), []byte("second"), 0o644))
	s.run()

	assert.Len(t, kindsSeen(t, ldir), 2,
		"an edited file inherited its predecessor's pass — the fingerprint half is not being applied")

	v, _ := s.check("f.md", "permits")
	assert.Equal(t, fingerprint.Of([]byte("second")), v.Fingerprint,
		"the stored verdict must track the content it was last reached about")
}

// TestPostDispatch_VerdictIsPerGuardrail is verdict_per_guardrail.
//
// One rule passing a file must not exempt another rule that has never seen it.
// Pooled per file, adding a guardrail would silently exempt everything the
// others had already judged.
func TestPostDispatch_VerdictIsPerGuardrail(t *testing.T) {
	proj := initRepo(t)
	ldir := ledgerDir(t)
	guardrailDir(t, proj, "first", `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./record.sh
---

# The rule that judges first
`, map[string]string{"record.sh": recordAndPass})
	guardrailDir(t, proj, "second", `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./record.sh
---

# A different rule, which has never seen this file
`, map[string]string{"record.sh": recordAndPass})

	s := newPostSession(t, proj)
	baselineAt(t, s.store, proj)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "x.md"), []byte("body"), 0o644))

	s.run()

	// Both ran, on the same file and the same content.
	assert.Len(t, kindsSeen(t, ldir), 2,
		"one guardrail's pass exempted another that had never judged this file")

	for _, name := range []string{"first", "second"} {
		v, found := s.check("x.md", name)
		assert.Truef(t, found, "no verdict stored for guardrail %q", name)
		assert.Truef(t, v.Passed, "guardrail %q should have passed", name)
	}
}

// TestPostDispatch_RetainedRefusalReFiresUntilTheContentChanges.
//
// The resolution of refusal_is_retained: a stored refusal is NOT an exemption,
// so the next cycle asks again, refuses again, and blocks again — for as long
// as the content stays as it is. That is the mechanism by which an
// after-the-fact rule gets a correction rather than complaining once.
//
// And the loop terminates on the agent's own action rather than on a counter:
// change the file and the fingerprint moves, so the rule is asked about the new
// content. Here the new content is judged by a rule that only objects to the
// original bytes, and the block stops.
func TestPostDispatch_RetainedRefusalReFiresUntilTheContentChanges(t *testing.T) {
	proj := initRepo(t)
	ledgerDir(t)
	// Refuses only the exact bytes "bad"; anything else passes.
	guardrailDir(t, proj, "picky", `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./judge.sh
  PostFileUpdate:
    - hooks:
        - type: command
          command: ./judge.sh
---

# Objects to one particular content
`, map[string]string{"judge.sh": `#!/bin/sh
cat >/dev/null
if grep -q bad "$SLOPRAIL_TEST_PROJECT/note.md" 2>/dev/null; then
  echo 'the file still says bad' >&2
  exit 1
fi
exit 0
`})

	s := newPostSession(t, proj)
	baselineAt(t, s.store, proj)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "note.md"), []byte("bad"), 0o644))

	// Cycle one: refused, and blocked.
	out1, _ := s.run()
	assert.Contains(t, out1, `"decision":"block"`, "the first cycle must block")
	v, found := s.check("note.md", "picky")
	require.True(t, found)
	require.False(t, v.Passed)

	// Cycle two, content UNCHANGED. The retained refusal is not an exemption,
	// so the rule is asked again and blocks again. This is the claim: a stored
	// failure must not be mistaken for settled work.
	out2, _ := s.run()
	assert.Contains(t, out2, `"decision":"block"`,
		"a retained refusal stopped re-firing — the violation went quiet with nothing fixed")

	// Cycle three: the agent fixed it. The fingerprint moved, so the rule is
	// asked about the new content, permits it, and the turn is no longer
	// blocked. The loop ends because the work was corrected.
	require.NoError(t, os.WriteFile(filepath.Join(proj, "note.md"), []byte("good"), 0o644))
	out3, _ := s.run()
	assert.NotContains(t, out3, `"decision":"block"`,
		"the block outlived the correction — the loop would never end")

	v, _ = s.check("note.md", "picky")
	assert.True(t, v.Passed, "the corrected content must be recorded as passing")
}

// TestPostDispatch_SubjectIsResolvedPerGuardrail.
//
// A Post subject is fingerprinted from the file ON DISK, and a hook is an
// arbitrary script that may rewrite the very file the event is about — a
// formatter bound to PostFileUpdate is the ordinary case. Resolve the subject
// once per event and the second rule's verdict is recorded against the FIRST
// rule's fingerprint, so a pass licenses bytes nobody judged.
//
// Here the first guardrail rewrites the file; the second's stored verdict must
// be keyed on what it actually saw.
func TestPostDispatch_SubjectIsResolvedPerGuardrail(t *testing.T) {
	proj := initRepo(t)
	ledgerDir(t)
	guardrailDir(t, proj, "aaa-rewrites", `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./rewrite.sh
---

# Rewrites the file it is asked about, the way a formatter would
`, map[string]string{"rewrite.sh": "#!/bin/sh\ncat >/dev/null\nprintf 'REWRITTEN' > \"$SLOPRAIL_TEST_PROJECT/doc.md\"\nexit 0\n"})
	guardrailDir(t, proj, "bbb-judges", `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./judge.sh
---

# Judges whatever is on disk when it is asked
`, map[string]string{"judge.sh": "#!/bin/sh\ncat >/dev/null\nexit 0\n"})

	s := newPostSession(t, proj)
	baselineAt(t, s.store, proj)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "doc.md"), []byte("original"), 0o644))

	s.run()

	// The rewrite really happened, or this proves nothing.
	body, err := os.ReadFile(filepath.Join(proj, "doc.md"))
	require.NoError(t, err)
	require.Equal(t, "REWRITTEN", string(body), "the first hook did not rewrite the file")

	// The second guardrail's verdict must be keyed on the bytes IT saw, not on
	// the ones the first guardrail was shown.
	v, found := s.check("doc.md", "bbb-judges")
	require.True(t, found)
	assert.Equal(t, fingerprint.Of([]byte("REWRITTEN")), v.Fingerprint,
		"the second rule's pass was recorded against content it never judged")
}

// TestPostDispatch_BlocksOnceWithEveryRefusal.
//
// Collected, not returned at the first. An agent handed one violation per turn
// spends as many turns as there are rules, and the rules bound after the first
// refusal would not have run at all.
func TestPostDispatch_BlocksOnceWithEveryRefusal(t *testing.T) {
	proj := initRepo(t)
	ledgerDir(t)
	for _, name := range []string{"alpha", "beta"} {
		guardrailDir(t, proj, name, `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./refuse.sh
---

# Refuses
`, map[string]string{"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho 'objection from " + name + "' >&2\nexit 1\n"})
	}

	s := newPostSession(t, proj)
	baselineAt(t, s.store, proj)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "f.md"), []byte("x"), 0o644))

	stdout, _ := s.run()

	assert.Equal(t, 1, strings.Count(stdout, `"decision":"block"`),
		"the cycle must block exactly once, however many rules objected")
	assert.Contains(t, stdout, "alpha", "the first guardrail's objection is missing")
	assert.Contains(t, stdout, "beta",
		"the second guardrail's objection is missing — blocking at the first hides the rest")
}

// TestPostDispatch_ACleanCycleDoesNotBlock: the control.
//
// Everything above asserts that a refusal blocks. This asserts the other side:
// a cycle nothing objected to must end normally, or the engine would be
// refusing on its own behalf rather than any project's.
func TestPostDispatch_ACleanCycleDoesNotBlock(t *testing.T) {
	proj := initRepo(t)
	ledgerDir(t)
	guardrailDir(t, proj, "permits", `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./ok.sh
---

# Permits
`, map[string]string{"ok.sh": "#!/bin/sh\ncat >/dev/null\nexit 0\n"})

	s := newPostSession(t, proj)
	baselineAt(t, s.store, proj)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "f.md"), []byte("x"), 0o644))

	stdout, _ := s.run()
	assert.Empty(t, strings.TrimSpace(stdout), "a cycle nothing objected to must not block")
}
