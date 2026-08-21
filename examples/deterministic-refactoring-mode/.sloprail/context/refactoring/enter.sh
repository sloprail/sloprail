#!/usr/bin/env bash
# enter: decide whether a refactor has been declared, and if so extract its
# scope, printing it as this context's payload. Runs on EITHER trigger — a
# PreToolUse (any tool about to run) or a PostTagWrite (fires at Stop).
#
# NOTE on "declining": printing the scope REPLACES the payload; printing nothing
# on a clean exit does NOT decline — the engine reads "clean exit, empty stdout"
# as "activate, keep the prior payload" (the only ways to decline are a non-zero
# exit or an unmet require). So on a turn with no #refactor this enter leaves the
# context active with whatever payload it had (empty on first entry). That is
# harmless here: the file-guard ALSO requires an sr:moved-from marker (its real
# narrowing), and the completeness gate permits when declared_markers is empty.
#
# NOTE ON LIFECYCLE (2026-08): a context TRACKS state; it does not block. The
# "did every declared move actually land" completeness check is NOT here and is
# NOT in exit.sh (a context's exit is pure lifecycle and cannot refuse a Stop —
# only a gate blocks). That check lives in the paired Stop gate
# `gate/refactor-complete`, which reads the `declared_markers` this enter writes
# into the context payload. See that gate for the completeness semantics.
#
# A refactor is declared the way unit 12 describes: the agent states intent
# (#refactor) AND declares the SCOPE — the set of moves it commits to landing,
# fixed upfront ("don't know yet in which files, but they're already set"). We
# read that declaration out of the trajectory rather than from a CLI call the
# agent had to remember to make.
#
# WHAT THE SCOPE NAMES (the bug-3 design): each scope token is the FQN a landed
# `sr:moved-from` marker will carry — `<path>@<sha>:<start>-<end>` — NOT a
# logical nickname. A move writes `// sr:moved-from <path>@<sha>:<lines>`, so
# declaring the fqn up front is what lets the gate check for a literal
# correspondence ("a file carrying `sr:moved-from <this fqn>` exists") instead of
# guessing which landed marker a nickname meant. The marker convention itself is
# unchanged — kind stays `moved-from`, so the reconcile file-guard still matches
# `any(markers, .kind == "moved-from")`.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

# Has the agent declared a refactor this session, and not yet finished one?
# The declaration is a single message carrying the #refactor tag plus the
# scope it intends to land, e.g.:
#   #refactor scope=src/beta.go@<sha>:10-24,src/gamma.go@<sha>:3-9
# The tag is re-derived as a PostTagWrite event; the scope= list is free text on
# that same entry's assistant message, so take the LAST entry writing the
# refactor tag and read its text.
#
# The tag lives at `.events[].fields.tags[].label` on a normalized entry — the
# normalized event wire form is {kind, fields}, so the tags list is UNDER
# `.fields`, not directly on the event. (Reading `.tags[]` off the event — the
# original bug here — matched nothing, so the scope was never read and
# declared_markers came back empty.)
decl="$(sr-session trajectory normalize \
  --path "$transcript_path" \
  --events PostTagWrite \
  | jq -r '
      def msgtext:
        if type == "string" then .
        elif type == "array" then [.[] | select(.type? == "text") | .text] | join("")
        elif type == "object" then [(.content // [])[] | select(.type? == "text") | .text] | join("")
        else "" end;
      [ .[]
        | select(any(.events[]?;
            .kind == "PostTagWrite" and any(.fields.tags[]?; .label == "refactor")))
      ][-1] // {}
      | .message | msgtext')"

if [ -z "$decl" ]; then
  # No #refactor in the trajectory (yet). Print nothing: the context is left as it
  # was (see the NOTE above — this does not clear an already-open scope, and the
  # gate permits on an empty declared_markers).
  exit 0
fi

# Pull the declared fqn set out of the declaration. These are the moves the
# agent has committed to landing; the paired Stop gate later verifies every one
# actually appeared as an `sr:moved-from` marker whose content reconciles.
scope="$(printf '%s' "$decl" | grep -oE 'scope=[^ ]+' | head -1 | cut -d= -f2)"

# Activate: this JSON becomes context[refactoring].payload, readable by any
# guard whose match expression names this context, and by the Stop gate which
# reads .context.refactoring.payload.declared_markers off its own stdin.
jq -n --arg scope "$scope" \
  '{declared_markers: ($scope | split(",")), declared_at: "trajectory"}'
