#!/bin/sh
# Refuses a write under a configured path unless the matching skill was loaded
# earlier in this session.
#
# WHAT THIS IS: a plugin that owns its own PreToolUse hook — no GUARDRAIL.md, no
# engine dispatching it. Its config lives at .sloprail/require-skill/config.yaml
# in the CONSUMER's project, an array of {match, skills}; this script is the
# whole of the rule, low-level commands doing the two hard parts:
#
#   sr-file changes    — what this toolcall is about to do, and to which file,
#                         parsed the same way regardless of tool (Write, Edit,
#                         a shell redirection the engine resolved).
#   sr-session query    — what the agent actually did earlier this session, read
#                         from the transcript rather than trusted from what it
#                         SAYS. An agent can claim to have read a skill; a Skill
#                         tool_use either is or is not in the record, and no
#                         wording puts it there.
#
# EXIT 2 REFUSES. Not 1 — see sloprail-tasks's judge scripts and the note this
# repo already carries about Claude Code's PreToolUse contract: any non-zero
# OTHER than 2 reads as "ran, nothing to say," and the write proceeds.

set -u

payload="$(cat)"

command -v jq >/dev/null 2>&1 || {
  echo "sloprail-require-skill: needs jq, which is not on PATH. Refusing rather than permitting unchecked." >&2
  exit 2
}
command -v sr-file >/dev/null 2>&1 || {
  echo "sloprail-require-skill: needs sr-file, which is not on PATH. Refusing rather than permitting unchecked." >&2
  exit 2
}
command -v sr-session >/dev/null 2>&1 || {
  echo "sloprail-require-skill: needs sr-session, which is not on PATH. Refusing rather than permitting unchecked." >&2
  exit 2
}

# The config lives in the CONSUMER's project, not in this plugin's install
# directory — it is the project's own list of prefixes and skills, the thing
# the plugin exists to be configured BY.
#
# Not $CLAUDE_PLUGIN_ROOT and not a Claude Code-specific project variable: a
# PreToolUse hook's process runs WITH ITS CWD SET TO THE PROJECT, the ordinary
# way any hook finds its tree, so "." is already correct there. SR_WORKSPACE
# overrides it for a caller outside that harness — a test, another
# integration — where the process cwd is not guaranteed to be the project.
root="${SR_WORKSPACE:-.}"
config="$root/.sloprail/require-skill/config.yaml"
[ -f "$config" ] || exit 0

# The path this toolcall is about to touch, from whichever kind of change it
# produces — create, update, or delete all carry `path`, and this rule cares
# about the path alone, not the bytes.
path="$(printf '%s' "$payload" | sr-file changes 2>/dev/null | jq -rs '.[0].path // empty' 2>/dev/null)"
[ -n "$path" ] || exit 0

# config.yaml → JSON, once, with yq if present and a minimal awk fallback if
# not — this plugin's one dependency beyond POSIX tools is jq, and yq is not
# assumed. The fallback only understands this file's own shape (a top-level
# `require:` list of `match:`/`skills:` pairs), which is honest: it is not a
# YAML parser, it is a reader of the one shape this config is documented to be.
config_json="$(
  if command -v yq >/dev/null 2>&1; then
    # .require, not `.` — the file's top level is {require: [...]}, and yq's
    # own JSON rendering keeps that wrapper. The awk fallback below produces the
    # INNER array directly, because it never sees the wrapper in the first
    # place. Measured: without `.require` here, config_json is the object
    # {"require":[...]}, and every `.[] as $e | $e.match` downstream reads
    # "Cannot index array with string match" against the outer array — a
    # failure that looked like the glob logic being wrong when the two config
    # readers were disagreeing about the shape they hand back.
    yq -o=json '.require' "$config" 2>/dev/null
  else
    awk '
      /^require:/ { next }
      /^[[:space:]]*-[[:space:]]*match:/ {
        if (m != "") { printf "%s{\"match\":\"%s\",\"skills\":[%s]}", (n++ ? "," : ""), m, s }
        m = $0; sub(/^[[:space:]]*-[[:space:]]*match:[[:space:]]*"?/, "", m); sub(/"?[[:space:]]*$/, "", m)
        s = ""
        next
      }
      /skills:/ {
        line = $0
        sub(/^[[:space:]]*skills:[[:space:]]*\[/, "", line)
        sub(/\][[:space:]]*$/, "", line)
        n2 = split(line, parts, ",")
        s = ""
        for (i = 1; i <= n2; i++) {
          gsub(/[[:space:]]/, "", parts[i])
          s = s (i > 1 ? "," : "") "\"" parts[i] "\""
        }
      }
      END { if (m != "") printf "%s{\"match\":\"%s\",\"skills\":[%s]}", (n ? "," : ""), m, s }
    ' "$config" | awk 'BEGIN{printf "["} {printf "%s",$0} END{print "]"}'
  fi
)"
[ -n "$config_json" ] && [ "$config_json" != "[]" ] || exit 0

# Which skills this path requires, by matching `match` as a glob against the
# reported path — the same relative spelling `sr-file changes` reports, which is
# what makes a project's config portable across checkouts.
#
# ** first, protected behind a placeholder, THEN single *. Doing them in one
# pass in the obvious order — ** to .*, then * to [^/]* — corrupts the very
# thing the first substitution just inserted: the .* it produced contains a *,
# and the second gsub matches that too, turning "memories/topics/**" into
# "memories/topics/.[^/]*" instead of "memories/topics/.*". Measured: without
# the placeholder this rule matches nothing and refuses nothing, silently.
#
# `.` as a bound variable ($e), not read fresh inside select — select's own
# condition changes what `.` means partway through the pipe (test's `.` is $p,
# not the config entry), so `.match` read AFTER entering select resolves
# against the wrong value. Cannot-index-a-string is what that looks like when
# it breaks; binding $e before select is what keeps the entry addressable
# throughout.
required="$(
  printf '%s' "$config_json" | jq -r --arg p "$path" '
    [ .[] as $e
      | ($e.match | gsub("\\*\\*"; "@@DBLSTAR@@") | gsub("\\*"; "[^/]*") | gsub("@@DBLSTAR@@"; ".*")) as $re
      | select($p | test("^" + $re + "$"))
      | $e.skills[] ]
    | unique | .[]
  ' 2>/dev/null
)"
[ -n "$required" ] || exit 0

# What the agent actually did this session, read from the transcript — not
# trusted from what it says. --whole-session because this plugin has no
# engine-tracked read position of its own; re-reading the record on every
# guarded write is the honest cost of not having one.
loaded="$(
  printf '%s' "$payload" |
    sr-session query --whole-session --where 'type == "assistant"' 2>/dev/null |
    jq -r '
      [ .[]
        | (.message.content // [])
        | select(type == "array")
        | .[]
        | select(.type == "tool_use" and .name == "Skill")
        | .input.skill
      ] | unique | .[]
    ' 2>/dev/null
)"

missing=""
for skill in $required; do
  case "
$loaded
" in
    *"
$skill
"*) ;;
    *) missing="${missing}${missing:+, }$skill" ;;
  esac
done

[ -n "$missing" ] || exit 0

cat >&2 <<EOF
SKILL REQUIRED: writing '$path' requires the '$missing' skill to have been
loaded first, and this session's record holds no Skill tool_use naming it.

Invoke the Skill tool with that name, then retry the write. Stating that you
have read it is not what is checked — the session's own record is.
EOF
exit 2
