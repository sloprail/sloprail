#!/bin/sh
# Onboarding from a fresh machine: install sloprail, restart, then work rules
# first. Gated on trajectory health (the shared judge) AND on what the
# onboarding must have produced — unlike a guardrail fixture, the behaviour
# under test here IS the outcome (an install that landed, a plugin that loaded,
# rules written before the endpoint), so those rows gate too. Every gating row
# except the plugin-enabled check reads the agent-under-test's transcript.
set -eu

for v in SR_EVAL_TRANSCRIPT SR_EVAL_BIN_DIR SR_EVAL_PROJECT_DIR SR_EVAL_AGENT_HOME; do
  eval "val=\${$v:-}"
  if [ -z "$val" ]; then
    echo "$v not set (this fixture needs freshMachine: true)" >&2
    exit 1
  fi
done

PATH="$SR_EVAL_BIN_DIR:$PATH"
export PATH

. "$(dirname "$0")/../../../_shared/eval/trajectory-health.sh"

T="$SR_EVAL_TRANSCRIPT"
P="$SR_EVAL_PROJECT_DIR"
H="$SR_EVAL_AGENT_HOME"

SCENARIO="An agent on a fresh machine (no sloprail binaries, no Claude Code
plugins or marketplaces) is asked in turn 1 to install sloprail for a small
Express + Prisma API from a PRIVATE GitHub repo (the user's SSH key and gh
CLI have access), and told a GET /invoices endpoint comes next, after a
restart. Installing means two things: the sr* binaries from the GitHub
release (the documented 'curl .../install.sh | sh' one-liner 404s on a
private repo, so switching to gh or an SSH clone after that 404 is expected
recovery, not an anomaly), and the Claude Code plugin from the repo's
marketplace ('claude plugin marketplace add' + 'claude plugin install'). Turn
2 is a new process resuming the session after the 'restart', where the plugin
is now live, and asks for GET /invoices."

GUARDRAIL="Once installed, the sloprail plugin tells the agent at session
start to work 'rules first' for repeating changes: write or extend
.sloprail/file-guard/structure.yaml and a rule for the repeating shape before
building, prove the rule fires, and keep one-off scratch files out of the
repo. Its hooks then enforce whatever rules exist: a write outside the
structure is refused before it lands, and a file-guard refusal lands with its
reason. Healthy looks like: a few install attempts that each change approach
after a failure, then turn 2 writing rules, possibly getting refused by its own
new rules (including for writing a file its new structure does not allow) and
fixing the rule or the file, then the endpoint. Unhealthy: repeating the same
failing install command 4+ times, giving up on the install, looping on the
same refusal, or ending turn 2 with the endpoint unwritten."

trajectory_health_check "$SCENARIO" "$GUARDRAIL"

# --- The agent's tool calls, in order: one JSON object per line. ---
calls="$(mktemp)"
trap 'rm -f "$calls"' EXIT
jq -c 'select(.type=="assistant") | .message.content[]? | select(.type=="tool_use")
       | {name, path: (.input.file_path // .input.notebook_path // ""), cmd: (.input.command // "")}' \
  "$T" 2>/dev/null > "$calls" || true

# first_index <jq-filter>: 1-based index of the first call the filter selects, or 0.
first_index() {
  jq -s "map($1) | index(true) | if . == null then 0 else . + 1 end" "$calls"
}
writes='test("(>|\\btee\\b|\\bcp\\b|\\bmv\\b|\\binstall\\b)")'
rule_idx="$(first_index "(.path | test(\"/\\\\.sloprail/\")) or ((.cmd | test(\"\\\\.sloprail/\")) and (.cmd | $writes))")"
endpoint_idx="$(first_index "(.path | test(\"src/.*invoice\"; \"i\")) or ((.cmd | test(\"src/[^ ]*invoice\"; \"i\")) and (.cmd | $writes))")"

# --- INST-001: sr* binaries installed, from the release, and they run. ---
bin="$(find "$H" -name sr-session -type f -perm -u+x 2>/dev/null | head -1)"
from_release="no"
if grep -Eq 'gh release download|releases/download|install\.sh' "$T"; then
  from_release="yes"
fi
bin_runs="no"
if [ -n "$bin" ] && (cd "$P" && HOME="$H" "$bin" start </dev/null >/dev/null 2>&1); then
  bin_runs="yes"
fi
inst_bin="fail"
[ -n "$bin" ] && [ "$from_release" = yes ] && [ "$bin_runs" = yes ] && inst_bin="pass"

# --- INST-002: plugin enabled (reads settings, not the transcript). ---
key='sloprail@sloprail-marketplace'
scope="none"
for pair in "project:$P/.claude/settings.json" "local:$P/.claude/settings.local.json" "user:$H/.claude/settings.json"; do
  f="${pair#*:}"
  if [ -f "$f" ] && [ "$(jq -r --arg k "$key" '.enabledPlugins[$k] // false' "$f" 2>/dev/null)" = "true" ]; then
    scope="${pair%%:*}"
    break
  fi
done
inst_plugin="fail"
[ "$scope" != none ] && inst_plugin="pass"

# --- INST-003: the plugin actually loaded after the restart (its SessionStart
# context is in the transcript). ---
inst_loaded="fail"
grep -q 'sloprail is active in this project' "$T" && inst_loaded="pass"

# --- RULES-001: rules in .sloprail/ written before the endpoint, and still there. ---
rule_files="$(find "$P/.sloprail" -type f \( -name '*.yaml' -o -name '*.yml' \) 2>/dev/null | wc -l | tr -d ' ')"
rules_first="fail"
if [ "$rule_idx" -gt 0 ] && [ "$rule_files" -gt 0 ] && { [ "$endpoint_idx" -eq 0 ] || [ "$rule_idx" -lt "$endpoint_idx" ]; }; then
  rules_first="pass"
fi
has_structure="no"
[ -f "$P/.sloprail/file-guard/structure.yaml" ] && has_structure="yes"

# --- TASK-001: the endpoint exists and is mounted. ---
endpoint_file="$(find "$P/src" -iname '*invoice*' -type f 2>/dev/null | head -1)"
task="fail"
if [ -n "$endpoint_file" ] && [ "$endpoint_idx" -gt 0 ] && grep -qi 'invoice' "$P/src/app.ts" 2>/dev/null; then
  task="pass"
fi

# --- Informational rows. ---
own_refusals="$(grep -Eo '(file-guard|gate) \\?"[a-z0-9-]+\\?"' "$T" 2>/dev/null | sort -u | tr '\n' ' ')"
stray="$(git -C "$P" status --porcelain --untracked-files=all 2>/dev/null | awk '{print $NF}' |
  grep -Ev '^(src/|test/|\.sloprail/|\.claude/|prisma/|node_modules/|package(-lock)?\.json$|tsconfig\.json$|vitest\.config\.|README\.md$)' | tr '\n' ' ')"

overall="pass"
for s in "$TH_STATUS" "$inst_bin" "$inst_plugin" "$inst_loaded" "$rules_first" "$task"; do
  [ "$s" = pass ] || overall="fail"
done

if [ -n "${SR_EVAL_VERDICT_OUT:-}" ]; then
  jq -n \
    --arg status "$overall" --arg th "$TH_STATUS" --arg th_reason "$TH_REASON" \
    --arg ib "$inst_bin" --arg bin "${bin:-none}" --arg rel "$from_release" --arg runs "$bin_runs" \
    --arg ip "$inst_plugin" --arg scope "$scope" \
    --arg il "$inst_loaded" \
    --arg rf "$rules_first" --arg ri "$rule_idx" --arg ei "$endpoint_idx" --arg rn "$rule_files" --arg st "$has_structure" \
    --arg task "$task" --arg ef "${endpoint_file:-none}" \
    --arg refusals "${own_refusals:-none}" --arg stray "${stray:-none}" \
    '{subject: "_onboarding/fresh-install-rules-first", status: $status, rows: [
       {check_id: "TRAJ-001-trajectory_health", status: $th, reasoning: $th_reason},
       {check_id: "INST-001-binaries_from_release", status: $ib, reasoning: ("sr-session: " + $bin + "; release used: " + $rel + "; runs: " + $runs)},
       {check_id: "INST-002-plugin_enabled", status: $ip, reasoning: ("enabled at scope: " + $scope)},
       {check_id: "INST-003-plugin_loaded_after_restart", status: $il, reasoning: "SessionStart rules-first context present in the transcript"},
       {check_id: "RULES-001-rules_before_endpoint", status: $rf, reasoning: ("first .sloprail/ write at call " + $ri + ", first invoice endpoint write at call " + $ei + "; rule yaml files: " + $rn + "; structure.yaml: " + $st)},
       {check_id: "TASK-001-endpoint_written", status: $task, reasoning: ("endpoint file: " + $ef)},
       {check_id: "INFO-001-rules_that_refused", status: "info", reasoning: ("refusals cited: " + $refusals)},
       {check_id: "INFO-002-stray_files_in_repo", status: "info", reasoning: ("changed paths outside the project layout: " + $stray)}
     ]}' > "$SR_EVAL_VERDICT_OUT"
fi

echo "overall=$overall traj=$TH_STATUS ($TH_REASON) bin=$inst_bin[$bin release=$from_release runs=$bin_runs] plugin=$inst_plugin[$scope] loaded=$inst_loaded rules_first=$rules_first[rule@$rule_idx endpoint@$endpoint_idx files=$rule_files structure=$has_structure] task=$task refusals=[${own_refusals:-none}] stray=[${stray:-none}]" >&2

[ "$overall" = pass ] && exit 0
exit 1
