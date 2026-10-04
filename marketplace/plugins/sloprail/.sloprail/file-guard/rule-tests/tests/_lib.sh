#!/usr/bin/env bash
# Helpers the cases' setup.sh source: they build a small project rule, `no-curl`, inside the sandbox
# repository, so a case can then change it the way a commit would. Rules are written with heredocs
# on purpose: a fixture file NAMED gate.yaml under tests/ would be read as a misplaced declaration.

# base_commit — the world before the work: one committed file, tagged `base`.
base_commit() {
  echo "hello" >README.md
  git add -A
  git commit -q -m "the project before the rule"
  git tag base
}

# write_no_curl — a gate that refuses a `curl` command line.
write_no_curl() {
  local d=.sloprail/gate/no-curl
  mkdir -p "$d"
  cat >"$d/gate.yaml" <<'YAML'
on:
  - event: PreCommandInvoke
    match: any(event.invocations, .bin == "curl")
checks:
  - script: ./refuse.sh
YAML
  cat >"$d/refuse.sh" <<'SH'
#!/usr/bin/env bash
set -uo pipefail
cat >/dev/null
jq -n '{reason: "use the fetch tool, not curl"}'
exit 1
SH
}

# write_no_curl_case NAME EXPECT COMMAND — a trajectory case: the command line is dispatched as a
# PreCommandInvoke and the gate must answer EXPECT.
write_no_curl_case() {
  local d=.sloprail/gate/no-curl/tests/$1
  mkdir -p "$d"
  printf 'expect: %s\n' "$2" >"$d/case.yaml"
  printf -- '- kind: PreCommandInvoke\n  command: %s\n' "'$3'" >"$d/trajectory.yaml"
  printf 'echo hi > README.md\ngit add -A\ngit commit -q -m base\n' >"$d/setup.sh"
}

# write_no_curl_tests — one refusing case and one permitting case.
write_no_curl_tests() {
  write_no_curl_case refuses-curl refuse "curl -s https://example.com"
  write_no_curl_case permits-ls permit "ls -la"
}

rule_tests_cases_loaded=1
