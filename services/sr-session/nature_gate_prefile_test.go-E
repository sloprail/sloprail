package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/module/modules"
	"github.com/sloprail/sloprail/internal/natures"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// Prevention is a gate's job: these pin the pre-write dispatch of gates over the
// file events of ONE tool call, which replaced the file-guard `preventive:` path.

// runPre runs gates over pre events with the state a dispatch would hold.
func runPre(t *testing.T, gates []declaration.Gate, events []event.Event, notes resolveNotes) []gateResult {
	t.Helper()
	reg, err := modules.Registry()
	require.NoError(t, err)
	store, err := sessionstate.Open(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return runGatesForEvents(discard(), reg, gates, events, hookScope{}, store,
		map[string]natures.ContextState{}, map[string]natures.GateState{}, notes)
}

func preWrite(kind, path string) event.Event {
	return event.Event{Kind: kind, Fields: map[string]any{
		filemod.FieldPath: path, filemod.FieldResultKnown: true, filemod.FieldNewContent: "x",
	}}
}

func writeGate(name, dir, script, match string, kinds ...string) declaration.Gate {
	g := declaration.Gate{Name: name, Dir: dir, Checks: []declaration.Check{{Script: script}}}
	for _, k := range kinds {
		g.On = append(g.On, declaration.GateTrigger{Event: k, Match: match})
	}
	return g
}

// A gate is asked about EVERY file a call changes: the first passes, the second
// and third fail, and the results name both failing files and not the passing one.
func TestRunGatesForEvents_ChecksEveryFileOfTheCall(t *testing.T) {
	dir := t.TempDir()
	ledger := filepath.Join(dir, "ledger")
	writeExecutable(t, dir, "bad.sh", `#!/bin/sh
p="$(cat)"
path="$(printf '%s' "$p" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p')"
echo "$path" >> "`+ledger+`"
case "$path" in *bad*) echo '{"reason":"BAD FILE"}'; exit 1 ;; esac
exit 0
`)
	g := writeGate("no-bad", dir, "./bad.sh", `event.path startsWith "src/"`, declaration.AliasPreFileWrite)
	events := []event.Event{
		preWrite(declaration.KindPreFileCreate, "src/ok.go"),
		preWrite(declaration.KindPreFileCreate, "src/bad1.go"),
		preWrite(declaration.KindPreFileUpdate, "src/bad2.go"),
	}

	results := runPre(t, []declaration.Gate{g}, events, resolveNotes{})
	reason := gateRefusal(results, events, "")
	require.NotEmpty(t, reason, "a call whose later files fail must be refused")
	assert.Contains(t, reason, "src/bad1.go")
	assert.Contains(t, reason, "src/bad2.go")
	assert.Contains(t, reason, "BAD FILE")
	assert.NotContains(t, reason, "src/ok.go", "a file the gate passed is not named as refused")

	b, err := os.ReadFile(ledger)
	require.NoError(t, err)
	assert.Equal(t, "src/ok.go\nsrc/bad1.go\nsrc/bad2.go\n", string(b), "the check is handed every file, in order")
}

// Every gate is asked about every file it selects, even one another gate already
// refused (each gate's own ledger and verdict stand), but the deny names only the
// first refusal of a file.
func TestRunGatesForEvents_SecondGateIsAskedButOnlyTheFirstRefusalIsHeard(t *testing.T) {
	dir := t.TempDir()
	ledgerB := filepath.Join(dir, "ledgerB")
	writeExecutable(t, dir, "refuse.sh", `#!/bin/sh
p="$(cat)"
path="$(printf '%s' "$p" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p')"
case "$path" in *bad*) echo '{"reason":"REFUSED BY A"}'; exit 1 ;; esac
exit 0
`)
	writeExecutable(t, dir, "refuse-b.sh", `#!/bin/sh
p="$(cat)"
path="$(printf '%s' "$p" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p')"
echo "$path" >> "`+ledgerB+`"
echo '{"reason":"REFUSED BY B"}'; exit 1
`)
	a := writeGate("a", dir, "./refuse.sh", `event.path startsWith "src/"`, declaration.AliasPreFileWrite)
	b := writeGate("b", dir, "./refuse-b.sh", `event.path startsWith "src/"`, declaration.AliasPreFileWrite)
	events := []event.Event{preWrite(declaration.KindPreFileCreate, "src/bad.go"), preWrite(declaration.KindPreFileCreate, "src/other.go")}

	reason := gateRefusal(runPre(t, []declaration.Gate{a, b}, events, resolveNotes{}), events, "")
	assert.Contains(t, reason, "REFUSED BY A")
	assert.Equal(t, 1, strings.Count(reason, "REFUSED BY A"), "a file refused twice is heard once")
	assert.Equal(t, 1, strings.Count(reason, "REFUSED BY B"), "b's refusal of src/bad.go is dropped; only its refusal of src/other.go is heard")
	assert.Contains(t, reason, "src/other.go")

	got, err := os.ReadFile(ledgerB)
	require.NoError(t, err)
	assert.Equal(t, "src/bad.go\nsrc/other.go\n", string(got), "gate b is asked about both files")
}

// A gate on PreFileDelete is handed the bytes about to be lost, and refuses the
// delete before it runs.
func TestRunGatesForEvents_PreFileDeleteGateSeesTheLostContent(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "keep.sh", `#!/bin/sh
p="$(cat)"
old="$(printf '%s' "$p" | jq -r '.event.oldContent')"
known="$(printf '%s' "$p" | jq -r '.event.oldContentKnown')"
echo "{\"reason\":\"LOSES $old (known=$known)\"}"
exit 1
`)
	g := writeGate("keep", dir, "./keep.sh", `event.path startsWith "src/"`, declaration.KindPreFileDelete)
	del := event.Event{Kind: declaration.KindPreFileDelete, Fields: map[string]any{
		filemod.FieldPath: "src/a.go", filemod.FieldOldContent: "PRECIOUS", filemod.FieldOldContentKnown: true,
		filemod.FieldOldMarkers: []any{},
	}}
	results := runPre(t, []declaration.Gate{g}, []event.Event{del}, resolveNotes{})
	require.Len(t, results, 1)
	assert.True(t, results[0].Refused)
	assert.Contains(t, results[0].Reason, "LOSES PRECIOUS (known=true)")
	assert.Equal(t, "src/a.go", results[0].Path)
}

// A gate sees an underivable write as resultKnown false — it is not silently
// handed an empty file — and sr-file's own words about the change are quoted with
// its refusal.
func TestRunGatesForEvents_UnderivableWriteIsVisibleAndQuotesSrFile(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "known.sh", `#!/bin/sh
p="$(cat)"
[ "$(printf '%s' "$p" | jq -r '.event.resultKnown')" = "true" ] && exit 0
echo '{"reason":"cannot verify this write"}'
exit 1
`)
	g := writeGate("known", dir, "./known.sh", `event.path startsWith "src/"`, declaration.AliasPreFileWrite)
	unknown := event.Event{Kind: declaration.KindPreFileUpdate, Fields: map[string]any{filemod.FieldPath: "src/x.go", filemod.FieldResultKnown: false}}
	notes := resolveNotes{failed: []resolveFailure{{path: "src/x.go", said: "SR-FILE-SAID-THIS"}}}

	results := runPre(t, []declaration.Gate{g}, []event.Event{unknown}, notes)
	require.Len(t, results, 1)
	assert.Contains(t, results[0].Reason, "cannot verify this write")
	assert.Contains(t, results[0].Reason, "SR-FILE-SAID-THIS")

	known := preWrite(declaration.KindPreFileUpdate, "src/x.go")
	assert.Empty(t, runPre(t, []declaration.Gate{g}, []event.Event{known}, notes), "a known result passes")
}

// A single-file call's one refusal keeps its wording exactly, and a multi-file
// call names each refused file.
func TestGateRefusal_SingleFileKeepsItsWordingMultiFileNamesEach(t *testing.T) {
	one := []event.Event{preWrite(declaration.KindPreFileCreate, "a.go")}
	got := gateRefusal([]gateResult{{Refused: true, Reason: "nope", Attribution: `"g"`, Path: "a.go"}}, one, "")
	assert.Equal(t, `nope (gate "g")`, got)

	many := []event.Event{preWrite(declaration.KindPreFileCreate, "a.go"), preWrite(declaration.KindPreFileCreate, "b.go")}
	got = gateRefusal([]gateResult{
		{Refused: true, Reason: "nope", Attribution: `"g"`, Path: "a.go"},
		{Refused: true, Reason: "nope", Attribution: `"g"`, Path: "b.go"},
	}, many, "")
	assert.Equal(t, 1, strings.Count(got, "nope"), "a reason shared by several files is said once")
	assert.Contains(t, got, "a.go, b.go")

	assert.Empty(t, gateRefusal(nil, one, ""))
}

// writeGateYAML installs a committed gate under a project, the gate twin of
// writeFileGuardYAML.
func writeGateYAML(t *testing.T, proj, name, yaml string, scripts map[string]string) {
	t.Helper()
	dir := filepath.Join(proj, ".sloprail", "gate", name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gate.yaml"), []byte(yaml), 0o644))
	for file, body := range scripts {
		require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(body), 0o755))
	}
	runGit(t, proj, "add", "-f", ".sloprail")
	runGit(t, proj, "commit", "-m", "gate "+name)
}
