package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// fixtureHooks are small hook scripts written the way a plugin's are, one for each
// of the four jobs T026_07 and T026_08 check — a `when` predicate, a prepare that
// may skip the judge, a context's enter — over a Post kind's settled file. They
// stand in for scripts a project's own rules ship (the repo ships only a few
// itself), so authoring-slop's grep and the fail-closed-on-an-unread-file contract
// are held to the same idioms whoever writes the script.
var fixtureHooks = map[string]string{
	// rel path as authoring-slop sees it (`.sloprail/<nature>/<rule>/<file>`)
	".sloprail/file-guard/when-pinned/pinned.sh": `#!/usr/bin/env bash
# A when predicate (exit 0 APPLIES the requirement, exit 1 waives it) over a Post
# kind's settled file: a file the engine could not read (newContentKnown false) is
# unknown, so it applies when committed code pins the path with an sr:invariant
# marker, and is waived when nothing pins it (nothing is at stake).
set -uo pipefail

input="$(cat)"
path="$(printf '%s' "$input" | jq -r '.event.path // ""')"
if [ "$(printf '%s' "$input" | jq -r '.event.newContentKnown // false')" != "true" ]; then
  if git -C "${SR_WORKSPACE:-.}" grep -F "sr:invariant" -- . 2>/dev/null | grep -q -F "$path"; then
    exit 0
  fi
  exit 1
fi
content="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
printf '%s' "$content" | grep -q -F "sr:invariant" && exit 0
exit 1
`,
	".sloprail/file-guard/when-drops/drops.sh": `#!/usr/bin/env bash
# A when predicate over a Post kind's settled file: does the change drop a line?
# Exit 0 applies the requirement, exit 1 waives it. A file the engine could not
# read (newContentKnown false) is undecided, never "nothing dropped": it applies.
set -uo pipefail

input="$(cat)"
if [ "$(printf '%s' "$input" | jq -r '.event.newContentKnown // false')" != "true" ]; then
  exit 0
fi
old="$(printf '%s' "$input" | jq -r '.event.oldContent // ""')"
new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
dropped="$(comm -23 <(printf '%s' "$old" | sort -u) <(printf '%s' "$new" | sort -u) | grep -c . || true)"
[ "${dropped:-0}" -gt 0 ] && exit 0
exit 1
`,
	".sloprail/file-guard/prepare-judges/prepare.sh": `#!/usr/bin/env bash
# A prepare over a Post kind's settled file: skips the judge on a decided pure
# addition, otherwise hands the judge its context. A file the engine could not read
# (newContentKnown false) is undecidable, so the judge is asked, never skipped.
set -uo pipefail

input="$(cat)"
if [ "$(printf '%s' "$input" | jq -r '.event.newContentKnown // false')" != "true" ]; then
  jq -n '{additionalContext: {}}'
  exit 0
fi
old="$(printf '%s' "$input" | jq -r '.event.oldContent // ""')"
new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
dropped="$(comm -23 <(printf '%s' "$old" | sort -u) <(printf '%s' "$new" | sort -u) | grep -c . || true)"
if [ "${dropped:-0}" -eq 0 ]; then
  printf '{"skip": true}\n'
  exit 0
fi
jq -n '{additionalContext: {}}'
`,
	".sloprail/context/goal/enter.sh": `#!/usr/bin/env bash
# A context's enter on a settled goal.yaml: activate only when the goal is enabled.
# A file the engine could not read (newContentKnown false) counts as in force
# (activate), never as switched off.
set -uo pipefail

input="$(cat)"
goal_path="$(printf '%s' "$input" | jq -r '.event.path // ""')"
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
case "$kind" in
  PostFileCreate | PostFileUpdate)
    if [ "$(printf '%s' "$input" | jq -r '.event.newContentKnown // false')" != "true" ]; then
      jq -n --arg path "$goal_path" '{goal: "unread", goal_path: $path}'
      exit 0
    fi
    ;;
  PreFileCreate | PreFileUpdate)
    # The result is not derivable ahead of the write: defer to the Post kind.
    [ "$(printf '%s' "$input" | jq -r '.event.resultKnown // false')" = "true" ] || exit 0
    ;;
  *) exit 0 ;;
esac
content="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
enabled="$(printf '%s' "$content" | grep '^enabled:' | awk '{print $2}')"
[ "$enabled" = "true" ] || exit 0
jq -n --arg path "$goal_path" '{goal: "enabled", goal_path: $path}'
`,
}

// writeFixtureHooks lays fixtureHooks out under a temp dir, at their rel paths.
func writeFixtureHooks(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range fixtureHooks {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
