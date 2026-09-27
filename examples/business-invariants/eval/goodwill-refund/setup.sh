#!/bin/sh
# Pin the seed's Refund to SPEC.md's rule 2 the way the pin-invariants skill
# does: this project's absolute path, the sha of the commit SPEC.md is at, and
# rule 2's line. Neither the path nor the sha exists until the run, so the seed
# carries a placeholder and this fills it in.
set -eu

git add SPEC.md
git commit --quiet --no-gpg-sign -m "billing invariants"
pin="$(pwd)@$(git log -1 --format=%H -- SPEC.md):SPEC.md#L3-3"
sed "s|@SPEC_PIN@|$pin|" src/charge.go > src/charge.go.tmp
mv src/charge.go.tmp src/charge.go
grep -q "$pin" src/charge.go
