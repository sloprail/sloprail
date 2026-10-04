package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A `claude` that answers every judge with a fixed verdict: the stand-in for a real model in the
// one place the suite exercises --live-judges.
const rudeClaude = `#!/bin/sh
out=""
for arg in "$@"; do
  case "$arg" in
    *"Write your answer to the file "*)
      out="$(printf '%s' "$arg" | sed -n 's/.*Write your answer to the file \([^ ]*\)\. .*/\1/p' | head -1)"
      ;;
  esac
done
if [ -n "$out" ]; then
  cat > "$out" <<'EOF'
{"pass": false, "reasoning": "the live judge found it rude"}
EOF
fi
exit 0
`

// T001_40: --live-judges asks the real judges (here a stand-in model) instead of the canned
// ones, and checks the case's expectation against the LIVE verdict: the judge-accuracy run. The
// canned verdict is not used, and reason_contains (the model's wording) is skipped.
func TestT001_40_LiveJudgesAreCheckedAgainstTheCasesExpectation(t *testing.T) {
	p := newPolite(t)
	p.Shim("claude", rudeClaude)
	p.SetEnv("CLAUDECODE=1") // what names the harness to sr-agent, as in a session
	kase(p, "file-guard", "polite", "rude-memo-is-refused",
		"expect: refuse\nreason_contains: words the live judge will not use\njudges:\n  judge.md.j2: {pass: true}\n", memoSetup, "")
	kase(p, "file-guard", "polite", "claims-it-is-polite",
		"expect: permit\njudges:\n  judge.md.j2: {pass: true}\n", memoSetup, "")

	// canned: both cases agree with their stubs
	res := p.Test("polite")
	require.Equal(t, 1, res.Code, "reason_contains is checked against the canned judge's words:\n"+res.Output)

	// live: the model says the memo is rude. The case that expects a refusal passes (the canned
	// pass: true was not used); the one that expects a permit is the judge being wrong about it.
	res = p.Test("polite", "--live-judges")
	require.Equal(t, 1, res.Code, res.Output)
	require.Contains(t, res.Output, "PASS rude-memo-is-refused")
	require.Contains(t, res.Output, "FAIL claims-it-is-polite")
	require.Contains(t, res.Output, "expected the engine to permit, but it refused")
	require.Contains(t, res.Output, "the live judge found it rude")
}
