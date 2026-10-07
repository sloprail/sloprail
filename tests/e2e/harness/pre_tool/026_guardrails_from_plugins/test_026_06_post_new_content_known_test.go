package e2e

import (
	"strings"
	"testing"
)

// postReaderWithout handles the Post kinds and reads newContent there without
// ever consulting newContentKnown — so a settled file the engine could not
// read (a link to a FIFO, a file past the cap) reads as an empty one. It does
// consult resultKnown on the Pre kinds, so the older rule-2 floor stays silent
// and only the Post floor can catch it.
const postReaderWithout = `#!/bin/sh
payload="$(cat)"
kind="$(printf '%s' "$payload" | jq -r '.event.kind')"
case "$kind" in
  PreFileCreate|PreFileUpdate)
    [ "$(printf '%s' "$payload" | jq -r '.event.resultKnown')" = "true" ] || exit 0
    body="$(printf '%s' "$payload" | jq -r '.event.newContent')" ;;
  PostFileCreate|PostFileUpdate)
    body="$(printf '%s' "$payload" | jq -r '.event.newContent')" ;;
  *) exit 0 ;;
esac
if printf '%s' "$body" | grep -q TODO; then
  echo '{"reason":"remove the TODO"}'
  exit 1
fi
exit 0
`

// postReaderWith is the same script consulting newContentKnown on the Post
// kinds, and failing closed when it is false.
var postReaderWith = strings.Replace(postReaderWithout,
	`  PostFileCreate|PostFileUpdate)
    body=`,
	`  PostFileCreate|PostFileUpdate)
    if [ "$(printf '%s' "$payload" | jq -r '.event.newContentKnown')" != "true" ]; then
      echo '{"reason":"the settled file could not be read"}'
      exit 1
    fi
    body=`, 1)

// T026_06: a hook reading a Post kind's newContent without newContentKnown is
// refused (content-may-be-unresolvable), and the same hook consulting it is
// admitted.
func TestT026_06_PostReadersMustConsultNewContentKnown(t *testing.T) {
	if postReaderWith == postReaderWithout {
		t.Fatal("precondition: the fixed hook must differ from the slop one")
	}

	e := New(t)
	proj := e.Project()
	got := e.Run(proj, "s-026-06a", "write a guardrail hook", Turns("done",
		Write("w1", newFormatGuardHook, postReaderWithout),
	))
	if !got.Refused() || !strings.Contains(got.Output, "without .newContentKnown") {
		t.Errorf("a hook reading Post newContent without newContentKnown was not refused for it:\n%s", got.Output)
	}

	e = New(t)
	proj = e.Project()
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
	got = e.Run(proj, "s-026-06b", "write a guardrail hook", Turns("done",
		readSkillFirst(t, Write("w1", newFormatGuardHook, postReaderWith))...,
	))
	if got.Refused() {
		t.Errorf("a hook consulting newContentKnown was refused:\n%s", got.Output)
	}
}
