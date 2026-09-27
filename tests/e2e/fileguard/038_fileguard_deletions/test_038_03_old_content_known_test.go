package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A PreFileDelete says whether its oldContent was read (oldContentKnown). This
// guard refuses every delete and names the flag, so the refusal shows what the
// engine told it.
const echoKnownCheck = `#!/bin/sh
payload="$(cat)"
kind="$(printf '%s' "$payload" | jq -r '.event.kind')"
case "$kind" in
  PreFileDelete)
    known="$(printf '%s' "$payload" | jq -r '.event.oldContentKnown')"
    echo "{\"reason\":\"KNOWN=$known\"}"
    exit 1 ;;
esac
exit 0
`

// T038_08: a delete whose bytes were read says so, whichever way it is made.
// sr-file's delete built its own event and never set the flag, so every
// `sr-file delete` reached a rule as "not read" although the bytes were there.
func TestT038_08_ADeleteThatWasReadSaysSo(t *testing.T) {
	for _, command := range []string{
		"rm docs/pinned.md",
		"sr-file delete docs/pinned.md --cite:user 'delete the pinned doc'",
	} {
		t.Run(command, func(t *testing.T) {
			e, proj := project(t, echoKnownCheck, map[string]string{"echo-known": guardYAML("include", true)})
			res := e.Run(proj, "s-038-08", "delete the pinned doc", Turns("done",
				Bash("b1", command),
			))
			if !res.Saw("KNOWN=true") {
				t.Errorf("%s: the delete event did not say its bytes were read:\n%s", command, res.Output)
			}
		})
	}
}

// T038_09: deleting a link to a FIFO or a device returns — through rm and
// through sr-file. Reading such a file the plain way blocks the pre-tool hook
// forever (a FIFO waits for a writer) or never ends (/dev/zero); the delete is
// predicted with oldContentKnown false instead.
func TestT038_09_DeletingALinkToAFIFOReturns(t *testing.T) {
	for _, command := range []string{
		"rm docs/to-fifo.md docs/to-zero.md",
		"sr-file delete docs/to-fifo.md --cite:user 'delete the links'",
		"sr-file delete docs/to-zero.md --cite:user 'delete the links'",
	} {
		t.Run(command, func(t *testing.T) {
			e, proj := project(t, echoKnownCheck, map[string]string{"echo-known": guardYAML("include", true)})
			fifo := filepath.Join(t.TempDir(), "fifo")
			if err := syscall.Mkfifo(fifo, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(fifo, filepath.Join(proj, "docs", "to-fifo.md")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/dev/zero", filepath.Join(proj, "docs", "to-zero.md")); err != nil {
				t.Fatal(err)
			}
			done := make(chan string, 1)
			go func() {
				res := e.Run(proj, "s-038-09", "delete the links", Turns("done", Bash("b1", command)))
				done <- res.Output
			}()
			select {
			case out := <-done:
				if !strings.Contains(out, "KNOWN=false") {
					t.Errorf("%s: the link's delete was not predicted as unread:\n%s", command, out)
				}
			case <-time.After(60 * time.Second):
				t.Fatalf("%s: the session blocked reading a FIFO or a device", command)
			}
		})
	}
}
