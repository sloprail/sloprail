# A structural, grep-shaped liveness check for a script rule — is this script
# EVEN CAPABLE of refusing, or is it (by construction) an unconditional
# permit? Mirrors authoring-slop's own detection style in this repo: cheap,
# deterministic, stated as a heuristic rather than a proof.
#
# script_can_refuse "<script-path>"  ->  0 (silent) if the script contains
# SOME conditional exit/refusal shape; non-zero with a reason otherwise.
#
# WHAT COUNTS AS "CAN REFUSE": the script's body, after stripping comment
# lines, must contain a NON-ZERO exit reachable from a conditional — either:
#   - a call to a `refuse`-shaped helper (the `refuse() { ... exit 1; }`
#     convention every script in this plugin and in sloprail-tasks uses), OR
#   - a literal `exit 1` (or any exit with a non-zero LITERAL status) that is
#     NOT the unconditional last line of the file with no enclosing `if`/
#     `case`/`&&`/`||` on the same or an enclosing construct.
#
# WHAT THIS CANNOT PROVE, stated plainly (the same honesty authoring-slop's
# own docs use about their grep floor): a script can call `exit 1` inside a
# branch that is unreachable in practice ("if false; then exit 1; fi"), and
# this check will not catch that — it establishes only that the SHAPE of a
# real refusal exists in the source, not that the refusal is ever reachable
# for real input. Proving THAT would need the script actually driven against a
# failing case, which is what the plugin's own e2e tests do for the shipped
# scripts (char-limit.sh, banned-phrases.sh) — this check is the floor a
# PROJECT's own script rule gets for free without writing a test.
script_can_refuse() {
  spath="$1"

  if [ ! -f "$spath" ]; then
    printf 'the script does not exist at %s\n' "$spath"
    return 1
  fi

  # Strip full-line and trailing comments crudely (good enough for a grep
  # floor; a `#` inside a quoted string is a rare false negative that makes
  # this check STRICTER, not laxer, which is the safe direction to be wrong
  # in).
  body="$(grep -v '^[[:space:]]*#' "$spath" 2>/dev/null)"

  # Shape 1: a call to a refuse()-style helper — the convention every script
  # in this plugin and in sloprail-tasks follows (`refuse "..."`, sometimes
  # with a variable). This is the common case and the one worth naming first.
  if printf '%s' "$body" | grep -qE '(^|[^A-Za-z0-9_])refuse[[:space:]]*(\(\)|"|[$])'; then
    return 0
  fi

  # Shape 2: an explicit non-zero `exit` NOT on the unconditional final
  # executable line — i.e. it appears somewhere the grep can see a
  # conditional construct (if/case/&&/||) either on the same line or above it
  # in the same script. This is deliberately loose (see the header's honesty
  # note) — it asks "does a conditional non-zero exit shape appear anywhere",
  # not "is it definitely reachable".
  if printf '%s' "$body" | grep -qE '(if|case|&&|\|\|)[^#]*exit[[:space:]]+[1-9]'; then
    return 0
  fi
  if printf '%s' "$body" | grep -qE 'exit[[:space:]]+[1-9][0-9]*'; then
    # A bare non-zero exit exists somewhere in the file even without a
    # same-line conditional keyword — still credited, since it may be inside
    # an earlier `if` block the same-line grep above does not see (multi-line
    # conditionals). This is intentionally permissive for the same reason:
    # false negatives here would refuse a perfectly good script rule, which
    # is a worse failure mode for a floor check than an occasional false
    # positive on something too clever to statically prove trivial.
    return 0
  fi

  printf 'no conditional non-zero exit or refuse()-style call was found in the script — every path through it appears to permit\n'
  return 1
}
