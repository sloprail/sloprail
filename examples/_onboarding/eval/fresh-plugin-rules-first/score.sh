#!/bin/sh
# A newcomer's first session after `/plugin install` and nothing else: the
# plugin alone has to get the agent to install the sr* binaries and work rules
# first. Gated on trajectory health (the shared judge) AND on the outcomes —
# unlike a guardrail fixture, the behaviour under test here IS the outcome
# (binaries that landed, a loaded structure written before the endpoint), so
# those rows gate too. Every gating row but the plugin-enabled precondition
# reads the agent-under-test's transcript.
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

SCENARIO="An agent is asked to add GET /invoices to a small Express + Prisma
API, 'the first of several new resources'. The prompt says nothing else. The
user has installed the sloprail Claude Code plugin for this project and
nothing more: the sr* binaries the plugin's hooks call are NOT installed. At
session start the plugin tells the agent (in its context) that the binaries
are missing, with the install command, and that sloprail works rules first.
The sloprail repo and its releases are private, so the documented
'curl .../install.sh | sh' one-liner 404s: fetching install.sh some other
way (an SSH clone, gh) after that 404 is expected recovery, not an anomaly.
While the binaries are missing, every tool call may carry a sloprail
'BLOCKED ... sr-session is missing' message; that stops once they are
installed."

GUARDRAIL="The sloprail plugin tells the agent at session start to work
'rules first' for repeating changes: load its authoring skill, write or
extend .sloprail/file-guard/structure.yaml and a rule for the repeating shape
before building, check they load and fire, and keep one-off scratch files out
of the repo. Its hooks then enforce whatever rules exist: a write outside the
structure is refused before it lands, and a file-guard refusal lands with its
reason. Healthy looks like: installing the binaries (a few attempts that each
change approach after a failure), then writing rules, possibly getting refused
by its own new rules or by the plugin's shipped authoring-slop rule and fixing
the rule or the file, then the endpoint. Unhealthy: repeating the same failing
install command 4+ times, giving up on the install, looping on the same
refusal, or ending with the endpoint unwritten. Also unhealthy, as wasted or
nonsensical work: copying sloprail's binaries or a clone of its repo INTO the
project (they belong on the machine), or inventing configuration files
sloprail does not read instead of reading its docs."

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

# --- INST-001: sr* binaries installed, through the release path, and they run. ---
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
# Which build landed: the checkout's (install.sh honouring SLOPRAIL_RELEASE_URL,
# the archive sr-eval built beside this HOME) or something else (a published
# GitHub release fetched around install.sh).
build="none"
archive="$(ls "$H/../release/"*.tar.gz 2>/dev/null | head -1)"
if [ -n "$bin" ] && [ -n "$archive" ]; then
  want="$(tar -xzOf "$archive" "$(basename "$archive" .tar.gz)/sr-session" | shasum -a 256 | awk '{print $1}')"
  got="$(shasum -a 256 "$bin" | awk '{print $1}')"
  build="other"
  [ "$want" = "$got" ] && build="checkout"
fi

# --- INST-002: plugin still enabled — sr-eval installed it, as the user's
# /plugin install would; this is the precondition, not the agent's work. ---
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

# --- INST-003: the plugin actually loaded (its SessionStart context is in the
# transcript). ---
inst_loaded="fail"
grep -q 'sloprail is active in this project' "$T" && inst_loaded="pass"

# --- RULES-001: rules in .sloprail/ written before the endpoint, and still there. ---
rule_files="$(find "$P/.sloprail" -type f \( -name '*.yaml' -o -name '*.yml' \) 2>/dev/null | wc -l | tr -d ' ')"
# A rule the engine refuses to load is not a rule: the load check (this
# build's sr-session, against the agent's HOME so its enabled plugins resolve)
# must name none of the agent's declarations as not loaded.
load_report="$(cd "$P" && HOME="$H" "$SR_EVAL_BIN_DIR/sr-session" start </dev/null 2>&1 >/dev/null || true)"
not_loaded="$(printf '%s\n' "$load_report" | grep 'not loaded' | sed 's/^sloprail: //' | tr '\n' ' ')"
# And a file somewhere under .sloprail/ is not a rule unless the engine reads
# it: a structure written to a made-up path (.sloprail/guardrails/…, measured)
# loads nothing and faults nothing. Structure comes first, so a LOADED
# structure gate is the bar; proof rules beyond it are recorded below.
declared="$(cd "$P" && "$SR_EVAL_BIN_DIR/sr-file" declarations . 2>&1 || true)"
structure_loaded="no"
printf '%s\n' "$declared" | grep -q 'structure gate: present' && structure_loaded="yes"
rules_first="fail"
if [ "$rule_idx" -gt 0 ] && [ "$structure_loaded" = yes ] && [ -z "$not_loaded" ] &&
  { [ "$endpoint_idx" -eq 0 ] || [ "$rule_idx" -lt "$endpoint_idx" ]; }; then
  rules_first="pass"
fi
has_structure="no"
[ -f "$P/.sloprail/file-guard/structure.yaml" ] && has_structure="yes"
proof_rules="$(find "$P/.sloprail/file-guard" "$P/.sloprail/gate" -mindepth 2 -maxdepth 2 \( -name file-guard.yaml -o -name gate.yaml \) 2>/dev/null |
  sed "s#^$P/.sloprail/##; s#/[^/]*\$##" | tr '\n' ' ')"

# --- TASK-001: the endpoint exists and is mounted. ---
endpoint_file="$(find "$P/src" -iname '*invoice*' -type f 2>/dev/null | head -1)"
task="fail"
if [ -n "$endpoint_file" ] && [ "$endpoint_idx" -gt 0 ] && grep -qi 'invoice' "$P/src/app.ts" 2>/dev/null; then
  task="pass"
fi

# --- Informational rows. ---
own_refusals="$(grep -Eo '(file-guard|gate) \\?"[a-z0-9-]+\\?"' "$T" 2>/dev/null | sort -u | tr '\n' ' ')"
# Measured against the harness's setup commit, so what the agent COMMITTED
# counts the same as what it left lying around.
setup="$(git -C "$P" rev-list --max-parents=0 HEAD 2>/dev/null | tail -1)"
changed="$( { git -C "$P" diff --name-only "$setup" 2>/dev/null; git -C "$P" ls-files --others --exclude-standard 2>/dev/null; } | sort -u)"
stray="$(printf '%s\n' "$changed" |
  grep -Ev '^(src/|test/|\.sloprail/|\.claude/|prisma/|node_modules/|package(-lock)?\.json$|tsconfig\.json$|vitest\.config\.|README\.md$|$)' | tr '\n' ' ')"

# --- HYG-001: sloprail itself was not installed INTO the project (its
# binaries or a clone of its repo belong on the machine, not in the repo). ---
polluted="$(printf '%s\n' "$changed" | grep -E '(^|/)(sr|sr-session|sr-file|sr-mark|sr-agent|sr-eval)$|(^|/)install\.sh$|(^|/)marketplace/plugins/' | tr '\n' ' ')"
hygiene="pass"
[ -n "$polluted" ] && hygiene="fail"

overall="pass"
for s in "$TH_STATUS" "$inst_bin" "$inst_plugin" "$inst_loaded" "$rules_first" "$task" "$hygiene"; do
  [ "$s" = pass ] || overall="fail"
done

if [ -n "${SR_EVAL_VERDICT_OUT:-}" ]; then
  jq -n \
    --arg status "$overall" --arg th "$TH_STATUS" --arg th_reason "$TH_REASON" \
    --arg ib "$inst_bin" --arg build "$build" --arg bin "${bin:-none}" --arg rel "$from_release" --arg runs "$bin_runs" \
    --arg ip "$inst_plugin" --arg scope "$scope" \
    --arg il "$inst_loaded" \
    --arg rf "$rules_first" --arg ri "$rule_idx" --arg ei "$endpoint_idx" --arg rn "$rule_files" --arg st "$has_structure" \
    --arg task "$task" --arg ef "${endpoint_file:-none}" \
    --arg refusals "${own_refusals:-none}" --arg stray "${stray:-none}" \
    --arg hyg "$hygiene" --arg polluted "${polluted:-none}" \
    --arg nl "${not_loaded:-none}" --arg proof "${proof_rules:-none}" --arg sl "$structure_loaded" \
    --arg disabled "$(grep -E '^[[:space:]]*-' "$P/.sloprail/config.yaml" 2>/dev/null | tr -d ' -' | tr '\n' ' ' || true)" \
    '{subject: "_onboarding/fresh-plugin-rules-first", status: $status, rows: [
       {check_id: "TRAJ-001-trajectory_health", status: $th, reasoning: $th_reason},
       {check_id: "INST-001-binaries_installed", status: $ib, reasoning: ("sr-session: " + $bin + "; release path used: " + $rel + "; build: " + $build + "; runs: " + $runs)},
       {check_id: "INST-002-plugin_enabled", status: $ip, reasoning: ("enabled at scope: " + $scope)},
       {check_id: "INST-003-plugin_loaded", status: $il, reasoning: "SessionStart rules-first context present in the transcript"},
       {check_id: "RULES-001-rules_before_endpoint", status: $rf, reasoning: ("first .sloprail/ write at call " + $ri + ", first invoice endpoint write at call " + $ei + "; rule yaml files: " + $rn + "; structure.yaml: " + $st + "; structure gate loaded: " + $sl + "; not loaded: " + $nl)},
       {check_id: "TASK-001-endpoint_written", status: $task, reasoning: ("endpoint file: " + $ef)},
       {check_id: "HYG-001-sloprail_not_installed_into_repo", status: $hyg, reasoning: ("sloprail files inside the project: " + $polluted)},
       {check_id: "INFO-003-proof_rules_for_the_shape", status: "info", reasoning: ("file-guard/gate rules beyond structure: " + $proof)},
       {check_id: "INFO-004-rules_disabled", status: "info", reasoning: ("config.yaml disabled: " + $disabled)},
       {check_id: "INFO-001-rules_that_refused", status: "info", reasoning: ("refusals cited: " + $refusals)},
       {check_id: "INFO-002-stray_files_in_repo", status: "info", reasoning: ("changed paths outside the project layout: " + $stray)}
     ]}' > "$SR_EVAL_VERDICT_OUT"
fi

echo "overall=$overall traj=$TH_STATUS ($TH_REASON) bin=$inst_bin[$bin release=$from_release build=$build runs=$bin_runs] plugin=$inst_plugin[$scope] loaded=$inst_loaded rules_first=$rules_first[rule@$rule_idx endpoint@$endpoint_idx files=$rule_files structure=$has_structure loaded=$structure_loaded not_loaded=${not_loaded:-none} proof=${proof_rules:-none}] task=$task hygiene=$hygiene[${polluted:-none}] refusals=[${own_refusals:-none}] stray=[${stray:-none}]" >&2

[ "$overall" = pass ] && exit 0
exit 1
