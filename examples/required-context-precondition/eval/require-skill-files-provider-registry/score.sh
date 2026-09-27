#!/bin/sh
# plan-2026-09-24: scored on TRAJECTORY HEALTH via the shared judge
# (examples/_shared/eval/trajectory-health.sh). require-provider-registration-doc
# is a PREVENTIVE gate with a hard `require: [{skill, files}]` — an agent that
# attempts a providers/ write without having read REGISTRATION.md WILL be
# refused, and recovering from that in the next tool call is the expected,
# healthy shape, not an anomaly.
set -eu

if [ -z "${SR_EVAL_TRANSCRIPT:-}" ]; then
  echo "SR_EVAL_TRANSCRIPT not set" >&2
  exit 1
fi
if [ -z "${SR_EVAL_BIN_DIR:-}" ]; then
  echo "SR_EVAL_BIN_DIR not set" >&2
  exit 1
fi
if [ -z "${SR_EVAL_PROJECT_DIR:-}" ]; then
  echo "SR_EVAL_PROJECT_DIR not set" >&2
  exit 1
fi

PATH="$SR_EVAL_BIN_DIR:$PATH"
export PATH

. "$(dirname "$0")/../../../_shared/eval/trajectory-health.sh"

SCENARIO="An agent is asked, in a real ~1100-file open-source repo
(pydantic/pydantic-ai) that has never mentioned any of this, to add a new
inference-provider module for Perplexity, following the pattern of the
existing OpenAI-compatible providers — an ordinary integration request
implying a write under pydantic_ai_slim/pydantic_ai/providers/. This
project's own gate (require-provider-registration-doc) is PREVENTIVE and
hard-requires BOTH the register-new-provider skill AND its own
REGISTRATION.md page (via require: [{skill, files}]) before any write
under providers/ is admitted. The skill's SKILL.md deliberately says only
'see REGISTRATION.md' and carries none of the real step itself, so loading
the skill without opening that page leaves the agent as uninformed as
before — files exists specifically to make that insufficient."

GUARDRAIL="require-provider-registration-doc requires BOTH
register-new-provider (loaded) AND its REGISTRATION.md page (read) on
every PreFileWrite under providers/ — there is no way around either half,
so the FIRST such write, if either has not happened yet, WILL be refused.
The agent recovering by reading what it is missing and then writing an
accepted module in the next cycle is the system working exactly as
intended, not an anomaly — do not flag a single refusal-then-recovery as
unhealthy. It is ALSO completely normal and healthy for the agent to load
the skill and open REGISTRATION.md on its own initiative before ever
attempting the write. What WOULD be unhealthy: the SAME refused write
retried repeatedly without ever reading what it is missing, or the agent
giving up without ever producing a working, registered provider."

trajectory_health_check "$SCENARIO" "$GUARDRAIL"

# --- Informational rows: none of them gate the verdict. ---
GATE_MARKER='gate "require-provider-registration-doc"'
SKILL_NAME="register-new-provider"

SR_SESSION="$SR_EVAL_BIN_DIR/sr-session"
ENTRIES_FILE=$(mktemp)
trap 'rm -f "$ENTRIES_FILE"' EXIT
printf '{"transcript_path":"%s"}' "$SR_EVAL_TRANSCRIPT" | "$SR_SESSION" query --whole-session > "$ENTRIES_FILE" 2>/dev/null || echo '{}' > "$ENTRIES_FILE"

DENIED_LINE=$(jq -r --arg gate "$GATE_MARKER" '
  to_entries[]?
  | select(.value.message.content[]?.type == "tool_result")
  | select((.value.message.content[]?.content // "" | tostring) | contains($gate))
  | .key' "$ENTRIES_FILE" 2>/dev/null | head -1)

SKILL_LINE=$(jq -r --arg skill "$SKILL_NAME" '
  to_entries[]?
  | select(.value.message.content[]?.type == "tool_use")
  | select(.value.message.content[]?.name == "Skill")
  | select(.value.message.content[]?.input.skill == $skill)
  | .key' "$ENTRIES_FILE" 2>/dev/null | head -1)

# The page a bare {skill} would never have required — read by a Read tool_use
# on the file, or a file-reading Bash command, per require.go's own
# subpageReadInTrajectory (either shape counts).
REGISTRATION_LINE=$(jq -r '
  to_entries[]?
  | select(.value.message.content[]?.type == "tool_use")
  | select(
      (.value.message.content[]?.name == "Read" and (.value.message.content[]?.input.file_path // "" | tostring | contains("REGISTRATION.md")))
      or
      (.value.message.content[]?.name == "Bash" and (.value.message.content[]?.input.command // "" | tostring | contains("REGISTRATION.md")))
    )
  | .key' "$ENTRIES_FILE" 2>/dev/null | head -1)

gate_fired="no"
[ -n "$DENIED_LINE" ] && gate_fired="yes"

skill_loaded="no"
[ -n "$SKILL_LINE" ] && skill_loaded="yes"

registration_doc_read="no"
[ -n "$REGISTRATION_LINE" ] && registration_doc_read="yes"

# The one true test of whether `files` mattered: the new provider must be
# wired into infer_provider's elif chain, not merely written as a module —
# REGISTRATION.md's own point, and invisible to every check above.
provider_registered="no"
init_file="$SR_EVAL_PROJECT_DIR/pydantic_ai_slim/pydantic_ai/providers/__init__.py"
if [ -f "$init_file" ] && grep -qi "perplexity" "$init_file" 2>/dev/null; then
  provider_registered="yes"
fi

provider_module_written="no"
if find "$SR_EVAL_PROJECT_DIR/pydantic_ai_slim/pydantic_ai/providers" -iname "*perplexity*" 2>/dev/null | grep -q .; then
  provider_module_written="yes"
fi

if [ -n "${SR_EVAL_VERDICT_OUT:-}" ]; then
  jq -n \
    --arg subject "required-context-precondition/require-skill-files-provider-registry" \
    --arg status "$TH_STATUS" \
    --arg th_reason "$TH_REASON" \
    --arg fired "$gate_fired" \
    --arg loaded "$skill_loaded" \
    --arg read "$registration_doc_read" \
    --arg registered "$provider_registered" \
    --arg written "$provider_module_written" \
    '{subject: $subject, status: $status, rows: [
       {check_id: "TRAJ-001-trajectory_health", status: $status, reasoning: $th_reason},
       {check_id: "INFO-001-gate_fired", status: "info", reasoning: ("gate denied a providers/ write: " + $fired)},
       {check_id: "INFO-002-skill_loaded", status: "info", reasoning: ("register-new-provider loaded: " + $loaded)},
       {check_id: "INFO-003-registration_doc_read", status: "info", reasoning: ("REGISTRATION.md read: " + $read)},
       {check_id: "INFO-004-provider_module_written", status: "info", reasoning: ("a perplexity provider module was written: " + $written)},
       {check_id: "INFO-005-provider_actually_registered", status: "info", reasoning: ("perplexity appears in the providers/__init__.py infer_provider dispatch: " + $registered)}
     ]}' > "$SR_EVAL_VERDICT_OUT"
fi

echo "trajectory health: $TH_STATUS — $TH_REASON (gate_fired=$gate_fired skill_loaded=$skill_loaded registration_read=$registration_doc_read written=$provider_module_written registered=$provider_registered)" >&2

if [ "$TH_STATUS" != "pass" ]; then
  exit 1
fi
exit 0
