#!/usr/bin/env bash
# The gate's match found a `go test` or a `make` with an e2e-looking argument. Decide from argv:
# refuse `make test-e2e[-shard]`, and a `go test` over tests/ packages that is wide (`...`), more
# than 2 packages, or has no -run flag.
# Contract: stdin is the GateCheckPayload; exit 1 with {"reason": ...} refuses.
set -uo pipefail

payload="$(cat)"
reason='Don'"'"'t run the e2e suite locally (sequential, ~1h). Push the branch and let CI run it (16 parallel shards, ~10–15 min). Locally, run at most 2 e2e packages, each with -run naming the tests.'

# Fail closed: a payload jq cannot read is refused, not let through unchecked.
if ! bad="$(printf '%s' "$payload" | jq -e '
  def isgo: (.bin | split("/") | last) == "go" and (.argv // [] | index("test")) != null;
  def ismake: (.bin | split("/") | last) == "make" and any(.argv // [] | .[]; . == "test-e2e" or . == "test-e2e-shard");
  def e2epkg: test("^(\\./|\\.\\./)*tests(/|$)");
  def hasrun: any(.[]; . == "-run" or . == "--run" or . == "-test.run" or . == "--test.run"
                       or startswith("-run=") or startswith("--run=") or startswith("-test.run=") or startswith("--test.run="));
  [.event.invocations[]?
   | if ismake then 1
     elif isgo then
       ((.argv // [])[1:]) as $a
       | [$a[] | select(startswith("-") | not) | select(e2epkg)] as $pk
       | select(($pk | length) > 0)
       | select(any($pk[]; contains("...")) or ($pk | length) > 2 or ($a | hasrun | not))
       | 1
     else empty end] | length')"; then
  echo '{"reason":"no-local-e2e-suite could not read the command; refusing rather than letting a possible local e2e run through. '"$reason"'"}'
  exit 1
fi
[ "$bad" -gt 0 ] || exit 0

jq -n --arg r "$reason" '{reason: $r}'
exit 1
