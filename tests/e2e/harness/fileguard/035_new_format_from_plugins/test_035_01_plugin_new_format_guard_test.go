package e2e

import (
	"testing"
)

// The plugin under test ships a NEW-format gate and a NEW-format file-guard. Both
// live inside the plugin's own `.sloprail/` and are never copied into any project
// these tests create, so "it fired" and "it was discovered inside the plugin" are
// the same fact — there is no project copy that could account for it.

// preGateYAML is a PreFileWrite GATE: it fires at PRE-tool, blocking a
// not-fine write before it lands. That is the pre-tool half of "loads at both pre
// and Stop".
const preGateYAML = `on:
  - event: PreFileWrite
    match: event.path startsWith "secrets/"
checks:
  - script: ./check.sh
`

// afterGuardYAML is a file-guard: it fires at STOP, on the settled file's content,
// blocking the turn. That is the Stop half.
const afterGuardYAML = `match: "memories/**/*.md"
checks:
  - script: ./check.sh
`

// checkRefuseSecret refuses a write whose content holds the word SECRET, on
// whichever event it is handed (pre for the gate, post for the
// after-check). The refusal reason is the guard's own words, so a test can see
// them reach the agent.
const checkRefuseSecret = `#!/bin/sh
payload="$(cat)"
echo asked >> "$SR_GUARDRAIL_DIR/ledger"
if printf '%s' "$payload" | grep -q SECRET; then
  echo '{"reason":"this write holds a SECRET and is not fine"}'
  exit 1
fi
exit 0
`

// checkRefuseSecretFileGuard is checkRefuseSecret for an after-check file-guard: its ledger
// is kept in the plugin's root, outside its `.sloprail`, because a file-guard's verdicts are keyed by
// a hash of everything under `.sloprail`, so a check that writes there changes its own key
// between the run that stored a verdict and the verify that reads it.
const checkRefuseSecretFileGuard = `#!/bin/sh
payload="$(cat)"
echo asked >> "$SR_GUARDRAIL_DIR/../../../$(basename "$SR_GUARDRAIL_DIR").ledger"
if printf '%s' "$payload" | grep -q SECRET; then
  echo '{"reason":"this write holds a SECRET and is not fine"}'
  exit 1
fi
exit 0
`

// T035_01: the headline for pre-tool. A project that installed a plugin shipping a
// NEW-format GATE, and copied nothing, has that guard blocking a
// not-fine write BEFORE it lands — and the refusal NAMES THE PLUGIN.
//
// The plugin-naming half is question 3 of the old-format task, ported: a refusal
// names a guardrail so the reader can go and look at it, and for a shipped rule
// the name alone points at the project's own `.sloprail/file-guard/…`, where there
// is nothing — the file is inside an install directory the project never wrote to.
// Without the plugin in the message a user meets a rule they never wrote and
// cannot find.
func TestT035_01_PluginGateFiresAtPreToolAndNamesThePlugin(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	// The guard ships INSIDE the plugin. Nothing is written into proj/.sloprail.
	pluginRoot := e.EnablePluginShippingGate(proj, "acme-guards", "no-secrets", preGateYAML,
		map[string]string{"check.sh": checkRefuseSecret})

	res := e.Run(proj, "s-035-01", "write a secret file", Turns("done",
		Write("w1", "secrets/prod.txt", "the value is SECRET"),
	))

	if !res.Refused() {
		t.Fatalf("a NEW-format gate shipped in the installed plugin did not block the "+
			"pre-write — nothing discovered it, which is the whole defect this closes:\n%s", res.Output)
	}
	if e.Exists(proj, "secrets/prod.txt") {
		t.Errorf("the write LANDED despite the plugin gate refusing it — the work was not prevented")
	}
	if !res.Saw("not fine") {
		t.Errorf("the plugin guard's refusal reason did not reach the agent:\n%s", res.Output)
	}
	// Question 3. The refusal must say where the rule came from — the plugin.
	if !res.Saw("from plugin") || !res.Saw("acme-guards") {
		t.Errorf("the refusal never says the rule came from a plugin, so a user meets a rule they "+
			"never wrote and cannot find the file:\n%s", res.Output)
	}
	// And the guard actually ran inside the plugin (its ledger is in the plugin's
	// own folder, not the project's).
	if n := e.PluginGateLedger(pluginRoot, "no-secrets", "ledger"); n == 0 {
		t.Errorf("the plugin-shipped guard's check never ran")
	}
}

// T035_02: the negative control for pre-tool. The same project, the same path, a
// write carrying none of the refused shapes — permitted.
//
// Without this, T035_01 would pass against an engine that refused every write
// under secrets/ for any reason at all, including a bug. This makes the refusal
// above evidence about the RULE rather than about the path.
func TestT035_02_PluginGuardStillPermitsWhatItDoesNotObjectTo(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	e.EnablePluginShippingGate(proj, "acme-guards", "no-secrets", preGateYAML,
		map[string]string{"check.sh": checkRefuseSecret})

	res := e.Run(proj, "s-035-02", "write a clean secret reference", Turns("done",
		Write("w1", "secrets/prod.txt", "a reference to the vault"),
	))

	if res.Refused() {
		t.Fatalf("a write carrying none of the refused shapes was blocked — the plugin's guard is "+
			"refusing on the path rather than on the content:\n%s", res.Output)
	}
	if !e.Exists(proj, "secrets/prod.txt") {
		t.Errorf("the permitted write did not land")
	}
}

// T035_03: the headline for Stop. A plugin shipping a NEW-format AFTER-check
// file-guard blocks the TURN at Stop for a not-fine committed file — and the block
// NAMES THE PLUGIN. This is the Stop half of "loads at both pre and Stop": the
// same nature dispatch that ran at pre-tool also runs at Stop, and it must resolve
// and load the plugin's guard there too.
//
// The write LANDS and is committed (a file-guard judges commits and cannot undo
// them), but the turn is blocked, and the block text is read from the conversation
// record (BlockingErrorsFrom), where a Stop refusal actually travels. A plugin's
// rule lives outside the repository, so its range starts at the HEAD recorded when
// the session began.
func TestT035_03_PluginAfterCheckGuardFiresAtStopAndNamesThePlugin(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	e.EnablePluginShippingFileGuard(proj, "acme-guards", "no-secret-memories", afterGuardYAML,
		map[string]string{"check.sh": checkRefuseSecretFileGuard})

	e.Run(proj, "s-035-03", "write a memory with a secret", Turns("done",
		Write("w1", "memories/note.md", "the password is SECRET"),
	).ThenCommit("add the memory"))

	blocks := e.BlockingErrorsFrom(proj, "s-035-03", "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a NEW-format plugin after-check file-guard did not block the turn at Stop — " +
			"the Stop dispatch did not load the plugin's guard")
	}
	joined := ""
	for _, b := range blocks {
		joined += b + "\n"
	}
	if !containsStr(joined, "not fine") {
		t.Errorf("the plugin guard's refusal reason did not reach the agent at Stop:\n%s", joined)
	}
	// The block names the plugin the guard came from.
	if !containsStr(joined, "from plugin") || !containsStr(joined, "acme-guards") {
		t.Errorf("the Stop block never says the rule came from a plugin:\n%s", joined)
	}
}

// T035_04: the Stop control. A clean settled memory admits — the turn ends, no
// block — so T035_03 is evidence about the content, not about the path.
func TestT035_04_PluginAfterCheckGuardAdmitsFineFile(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	pluginRoot := e.EnablePluginShippingFileGuard(proj, "acme-guards", "no-secret-memories", afterGuardYAML,
		map[string]string{"check.sh": checkRefuseSecretFileGuard})

	e.Run(proj, "s-035-04", "write a clean memory", Turns("done",
		Write("w1", "memories/note.md", "a perfectly ordinary note"),
	).ThenCommit("add the memory"))

	blocks := e.BlockingErrorsFrom(proj, "s-035-04", "Stop")
	if len(blocks) != 0 {
		t.Errorf("a plugin after-check guard whose check passed blocked the turn anyway:\n%v", blocks)
	}
	// It DID run — it just passed (proving the pass is a real check, not a guard
	// that never fired at Stop).
	if n := e.PluginFileGuardLedger(pluginRoot, "no-secret-memories", "../../../no-secret-memories.ledger"); n == 0 {
		t.Errorf("the plugin after-check guard never ran on a matching settled file")
	}
}

// T035_05: a plugin's new-format guard the project switched off is inert.
//
// The consumer does not own the plugin's declaration — it lives in an install
// directory the next reinstall overwrites — so the switch-off is made from the
// project's own config, naming the rule by its qualified name
// `<plugin>/<nature>/<name>`. The write here is the one T035_01 proved is refused,
// so this is that test's own control inverted: the same plugin, the same content,
// one config file different.
// sr:proves loading/disabled-by-qualified-name
func TestT035_05_ADisabledPluginNewFormatGuardIsInert(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	e.EnablePluginShippingGate(proj, "acme-guards", "no-secrets", preGateYAML,
		map[string]string{"check.sh": checkRefuseSecret})
	// Disable it from the project's own config, by qualified name.
	e.WriteFile(proj, ".sloprail/config.yaml", "disabled:\n  - acme-guards/gate/no-secrets\n")

	res := e.Run(proj, "s-035-05", "write a secret file", Turns("done",
		Write("w1", "secrets/prod.txt", "the value is SECRET"),
	))

	if res.Refused() {
		t.Fatalf("a plugin new-format gate the project disabled still refused — a consumer who cannot "+
			"switch off a shipped rule can only uninstall the plugin:\n%s", res.Output)
	}
	if !e.Exists(proj, "secrets/prod.txt") {
		t.Errorf("the write did not land even though nothing refused it")
	}
}
