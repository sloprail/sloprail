# Citation resolving, sourced by every rule that has to read one.
#
# A citation is `<absolute-path>:<ranges>`, ranges being `N` or `N-M` and several
# of them comma-separated. The SHAPE is already guaranteed by task.cue's regex
# before any of this runs, so nothing here re-validates the spelling. Everything
# here asks what a schema cannot: does the path resolve, does the line exist, and
# is the cited entry the kind of record the citing field claims it is.
#
# ONE COPY, sourced, rather than the same awk pasted into three scripts. The body
# rule, the review rule and the stop rule must agree exactly about what line
# 40-60 means; if two of them disagreed, a task would satisfy one rule and be
# refused by another with no way to satisfy both.
#
# Nothing here exits. These are functions that print a reason and return
# non-zero, so the calling rule decides what a failure means — the body rule
# refuses at Pre, the stop rule reports at TurnEnd, and one broken citation has
# to be able to produce both.

# cite_path "<citation>" -> the path half.
#
# `%%:*` strips from the LAST colon backwards would be wrong; `%%` is the longest
# match from the right of the pattern `:*`, which for "/a/b.jsonl:12-30" removes
# ":12-30" and leaves the path. An absolute path cannot contain a colon under
# task.cue's regex ([^:]+), so there is exactly one colon to cut at.
cite_path() { printf '%s' "${1%%:*}"; }

# cite_ranges "<citation>" -> the ranges half.
cite_ranges() { printf '%s' "${1##*:}"; }

# cite_lines "<ranges>" -> every line number the ranges name, one per line.
#
# Expanded rather than kept as bounds because every caller wants the membership
# question ("is line 44 cited?"), so expanding once here means no caller writes
# its own loop over `N-M`. A reversed range (`60-40`) yields nothing, which
# cite_check catches by testing for an empty expansion.
#
# `printf '%s\n'` — WITH the trailing newline, and that is not cosmetic. Without
# it the last (usually only) range has no line terminator, `read` returns
# non-zero at EOF, and the shell DISCARDS the value it just read. The function
# then returns nothing for every well-formed single range, every citation looks
# reversed, and the rule refuses valid work while appearing to run.
#
# Measured, not theorised: `cite_lines '1-2'` returned empty and a task citing
# real files was refused with "the range names no lines". Caught only because the
# positive case was tested — the negative cases all passed, for the wrong reason.
cite_lines() {
  printf '%s\n' "$1" | tr ',' '\n' | while IFS= read -r part; do
    [ -n "$part" ] || continue
    case "$part" in
      *-*)
        lo="${part%%-*}"; hi="${part##*-}"
        # A REVERSED range is refused here rather than left to seq, because BSD
        # seq counts DOWN: `seq 5 2` on macOS prints 5 4 3 2 rather than nothing,
        # so `60-40` would silently become a valid set of lines and the citation
        # would pass. GNU seq prints nothing for the same input, so the rule
        # would have behaved one way on the author's machine and another on CI.
        # Measured on this machine, which is why it is a test rather than a note.
        [ "$lo" -le "$hi" ] 2>/dev/null || continue
        seq "$lo" "$hi" 2>/dev/null
        ;;
      *)   printf '%s\n' "$part" ;;
    esac
  done
}

# cite_check "<citation>" "<label>" -> prints a reason and returns 1 if the
# citation does not resolve. Silent, returns 0, when it does.
#
# The three failures are kept apart because they need different fixes: the path
# is wrong, the range is past the end, or the range is malformed.
cite_check() {
  citation="$1"
  label="$2"
  path="$(cite_path "$citation")"
  ranges="$(cite_ranges "$citation")"

  if [ ! -f "$path" ] || [ ! -r "$path" ]; then
    printf '%s %s — no readable file at that path\n' "$label" "$citation"
    return 1
  fi

  lines="$(cite_lines "$ranges")"
  if [ -z "$lines" ]; then
    printf '%s %s — the range names no lines (reversed, e.g. 60-40?)\n' "$label" "$citation"
    return 1
  fi

  # `wc -l` counts NEWLINES, so a file whose last line has no trailing newline is
  # undercounted by one. Rather than reason about that, the highest cited line is
  # compared against the count and then, only if it looks past the end, actually
  # read with sed. A line sed can produce exists whatever wc said.
  total="$(wc -l < "$path" | tr -d ' ')"
  for n in $lines; do
    [ "$n" -le "$total" ] 2>/dev/null && continue
    if [ -z "$(sed -n "${n}p" "$path" 2>/dev/null)" ]; then
      printf '%s %s — line %s is past the end of the file (%s lines)\n' \
        "$label" "$citation" "$n" "$total"
      return 1
    fi
  done
  return 0
}

# cite_check_user_message "<citation>" "<label>" -> the citation must name real
# USER messages in a .jsonl transcript.
#
# This is the deterministic half of the human-authored rule, and the reason it
# CAN be deterministic: a real user message is `type == "user"` whose
# `message.content` is a STRING. Tool results are also `type: "user"` and carry
# an ARRAY, so keying on the type alone would let an agent cite a tool result —
# its own output — as the human's ask, which is exactly the substitution the rule
# exists to prevent.
#
# Measured on a real transcript before this was written: 502 string-content user
# messages against 1996 array-content ones. The distinction is the majority of
# the file, not an edge case.
cite_check_user_message() {
  citation="$1"
  label="$2"
  path="$(cite_path "$citation")"

  case "$path" in
    *.jsonl) ;;
    *) printf '%s %s — a user-message citation must name a .jsonl transcript\n' "$label" "$citation"
       return 1 ;;
  esac

  if ! reason="$(cite_check "$citation" "$label")"; then
    printf '%s\n' "$reason"
    return 1
  fi

  for n in $(cite_lines "$(cite_ranges "$citation")"); do
    # THREE kinds are rejected here, not two, and the third was found the hard
    # way. `isCompactSummary` marks an entry the HARNESS wrote into the record
    # wearing `type: "user"`: a summary produced when the conversation ran out
    # of context, which quotes the user's earlier messages back verbatim after
    # the originals have been dropped from the transcript.
    #
    # That makes it the most dangerous thing on this list. It is `type: "user"`,
    # its content IS a string, and it genuinely contains the user's words — so
    # every other test here passes and it reads as authorisation. But nobody
    # typed it at that position: it is a model's summary, and an agent citing it
    # is citing prose another model produced.
    #
    # Measured: line 6840 of the reference transcript is a compaction summary
    # and was ACCEPTED by this function until this branch was added. The words
    # inside it are really the user's, which is exactly why the hole was
    # invisible — the citation "works" and points at something true.
    #
    # `isMeta` is checked alongside it for injected notes that are not
    # summaries. The field names are read from the record rather than guessed:
    # a first attempt tested `.isMeta` alone, which is null on this entry, and
    # the check silently passed while looking correct.
    kind="$(sed -n "${n}p" "$path" 2>/dev/null | jq -r '
      if (.isCompactSummary == true) then "harness-generated compaction summary"
      elif (.isMeta == true) then "harness-injected note"
      # HARNESS-INJECTED text wearing type: "user". These carry no isMeta and no
      # isCompactSummary flag, so nothing but the content itself distinguishes
      # them, and every one of them is machine-generated:
      #
      #   <task-notification>  a subagent reporting back. The single most
      #                        dangerous case, because it contains an AGENT
      #                        describing work — cite it and the agent is
      #                        literally quoting itself as the human.
      #   <system-reminder>    harness bookkeeping.
      #   Stop hook feedback   a guardrail refusal handed back to the agent,
      #                        i.e. THIS system, quoted as if it were an ask.
      #   Caveat:              harness preamble.
      #
      # Matching on a prefix is weak and is chosen deliberately over nothing: the
      # flags that would make this robust are absent on these records. A user who
      # genuinely opens a message with one of these markers is refused, which is
      # the safe direction.
      #
      # Measured: line 2482 of the reference transcript is a task-notification
      # and was ACCEPTED as human authorisation until this branch existed.
      elif (.type == "user" and (.message.content | type) == "string"
            and ((.message.content | ltrimstr(" "))
                 | startswith("<task-notification>")
                   or startswith("<system-reminder>")
                   or startswith("<local-command")
                   or startswith("Stop hook feedback")
                   or startswith("Caveat:")))
      then "harness-injected text, not something the human typed"
      elif (.type == "user" and (.message.content | type) == "string")
      then "user"
      # A QUEUED message is the user typing while the agent is still working.
      # The harness parks it as `type: "queue-operation"` with the text in
      # `.content`, and the agent consumes it from the queue — in the reference
      # transcript, WITHOUT a `type: "user"` record ever being written for it.
      # So the words are in the record and provably came from the human, but
      # they never appear at a line the test above would accept.
      #
      # Verified before this branch was added: searching every transcript in the
      # project for one such message found it only as a queue-operation, and
      # quoted inside a compaction summary. There is no user-typed record of it
      # anywhere else, so without this branch a real ask is permanently
      # uncitable.
      #
      # Only `enqueue` counts. `remove` is the harness dequeuing, which is
      # bookkeeping by the machine rather than anything a person typed. 630
      # enqueued messages in the reference transcript — a large class of genuine
      # asks, not an edge case.
      #
      # This widening was decided by the repo owner, asked for explicitly,
      # because it is the kind of loosening an agent must not make on its own:
      # the first version of this branch was written to make one citation pass
      # and was reverted for that reason.
      #
      # NOTE FOR EDITORS: this comment block sits inside a SINGLE-QUOTED jq
      # program. An apostrophe here closes that string and the whole file stops
      # parsing, which refuses every citation in the project. That happened once
      # already. Keep this block free of apostrophes.
      elif (.type == "queue-operation" and .operation == "enqueue"
            and (.content | type) == "string") then "user"
      elif .type == "user" then "tool-result"
      else (.type // "unknown") end' 2>/dev/null)"
    if [ "$kind" != "user" ]; then
      printf '%s %s — line %s is a %s entry, not a user message\n' \
        "$label" "$citation" "$n" "${kind:-unparseable}"
      return 1
    fi
  done
  return 0
}
