#!/usr/bin/env bash
# Sourced, never run: what a gate needs to say whether some commits are OWED, shared by
# no-merge-over-refusals and no-destroying-owed-work. A gate's folder is all a gate can rely
# on, so each holds its own copy; they are kept byte-identical (e2e T035_00 fails otherwise).
#
# A set of commits is owed when, anywhere in the session FAMILY (the root session's check
# results and every sub-agent's, `sr-checks sql --family`):
#   - a rule refused one of them (or an earlier commit of the same line of work that the
#     default branch does not hold yet) and no later finished pass at a descendant fixed
#     it; or
#   - the session recorded a commit on that branch that some file-guard has not passed yet,
#     so the Stop that would judge it has not run (an abandoned ref is nobody's debt).
#
# Caller sets before calling:
#   ws         the workspace
#   tips[]     the commit shas at stake
#   branches[] the branch names at stake (a recorded commit on one of them counts)
#   what       the command, for the message (optional)
# Functions:
#   owed_setup      finds the default branch; defines in_default
#   owed_load       reads the family's check results into $runs; returns 1 with
#                   $owed_error set when they cannot be read
#   owed_evaluate   sets $refusal_listing and $owed_listing (empty when nothing is owed)
#   owed_all        every recorded, non-abandoned tip and every refused head, for a
#                   command that endangers everything at once
#   owed_unique     de-duplicates tips[] and branches[]
#   owed_cite_hint  the way past, in words

owed_cite_hint="Only the user can say to go ahead anyway: cite their exact words, as one command, \`sr-session trajectory cite '<their exact words>' && <the command>\`."

owed_setup() {
  defbr=""
  local c
  for c in "$(git -C "$ws" symbolic-ref -q refs/remotes/origin/HEAD 2>/dev/null || true)" \
    refs/remotes/origin/main refs/remotes/origin/master refs/heads/main refs/heads/master; do
    [ -n "$c" ] || continue
    if git -C "$ws" rev-parse --verify -q "$c^{commit}" >/dev/null 2>&1; then
      defbr="$c"
      break
    fi
  done
  defnames="main master"
  [ -n "$defbr" ] && defnames="$defnames ${defbr##*/}"
  return 0
}

in_default() { [ -n "${defbr:-}" ] && git -C "$ws" merge-base --is-ancestor "$1" "$defbr" 2>/dev/null; }

owed_unique() {
  local x t2=() b2=()
  while IFS= read -r x; do t2+=("$x"); done < <(printf '%s\n' ${tips[@]+"${tips[@]}"} | awk 'NF && !s[$0]++')
  tips=(${t2[@]+"${t2[@]}"})
  while IFS= read -r x; do b2+=("$x"); done < <(printf '%s\n' ${branches[@]+"${branches[@]}"} | awk 'NF && !s[$0]++')
  branches=(${b2[@]+"${b2[@]}"})
}

# Each rule's latest run per judged head, per store: bad = a failing check or an engine
# failure; good = a finished, passing run.
owed_load() {
  owed_error=""
  runs="$(cd "$ws" && sr-checks sql --family "
    select r.check_id as rule, r.head_ref as head, r.run_at as run_at,
           case when r.exit_code != 0 or exists (select 1 from checks c where c.run_id = r.id and c.status in ('fail', 'error'))
                then 1 else 0 end as bad,
           case when r.exit_code = 0 and r.error is null and json_extract(r.metadata, '\$.state') = 'complete'
                     and not exists (select 1 from checks c where c.run_id = r.id
                                     and (c.status in ('fail', 'error', 'interrupted')
                                          or json_extract(c.metadata, '\$.staleFrom') is not null))
                then 1 else 0 end as good,
           coalesce((select c.kind from checks c where c.run_id = r.id and c.status in ('fail', 'error') limit 1), 'engine') as kind,
           coalesce((select json_extract(c.metadata, '\$.reasoning') from checks c where c.run_id = r.id and c.status in ('fail', 'error') limit 1),
                    r.error, '') as why
    from check_runs r
    where r.head_ref <> ''
      and r.id = (select r2.id from check_runs r2 where r2.check_id = r.check_id and r2.head_ref = r.head_ref
                  order by r2.run_at desc, r2.rowid desc limit 1)" 2>&1)"
  if ! printf '%s' "$runs" | jq -e 'type == "array"' >/dev/null 2>&1; then
    case "$runs" in
      *"no session to read"*) runs="[]" ;; # not in a session: nothing was ever recorded
      *)
        owed_error="${runs:0:200}"
        runs="[]"
        return 1
        ;;
    esac
  fi
  return 0
}

# The session's recorded refs, as "<ref> <tip> <abandoned-at or ->" lines.
owed_recorded() {
  [ -n "${SR_SESSION_ID:-}" ] || return 0
  (cd "$ws" && sr-session refs list --session "$SR_SESSION_ID" 2>/dev/null) |
    awk -F'\t' 'NF >= 3 {print $2, $3, ($5 == "" ? "-" : $5)}'
}

# related H: H is one of the commits at stake, or an earlier commit of the same line of work
# that the default branch does not hold yet.
owed_related() {
  local h="$1" t
  for t in ${tips[@]+"${tips[@]}"}; do
    [ "$h" = "$t" ] && return 0
  done
  in_default "$h" && return 1
  for t in ${tips[@]+"${tips[@]}"}; do
    git -C "$ws" merge-base --is-ancestor "$h" "$t" 2>/dev/null && return 0
  done
  return 1
}

owed_evaluate() {
  refusal_listing=""
  owed_listing=""
  local h rule kind at why g fixed t rname rtip rab hit b missing rules ok r recorded

  # (1) refusals
  local heads_bad
  heads_bad="$(printf '%s' "$runs" | jq -r '.[] | select(.bad == 1) | .head' | sort -u)"
  while IFS= read -r h; do
    [ -n "$h" ] || continue
    case "$h" in *[!0-9a-f]*) continue ;; esac
    owed_related "$h" || continue
    # Each rule that refused at H, unless a later finished pass at H or a descendant fixed it.
    while IFS=$'\t' read -r rule kind at why; do
      [ -n "$rule" ] || continue
      fixed=""
      while IFS= read -r g; do
        [ -n "$g" ] || continue
        if git -C "$ws" merge-base --is-ancestor "$h" "$g" 2>/dev/null; then fixed=1; break; fi
      done < <(printf '%s' "$runs" | jq -r --arg r "$rule" --arg at "$at" '.[] | select(.rule == $r and .good == 1 and .bad == 0 and (.run_at | tostring) > $at) | .head')
      [ -n "$fixed" ] && continue
      refusal_listing="${refusal_listing:+$refusal_listing$'\n'}  - $rule ($kind) at ${h:0:12}: $(printf '%s' "$why" | head -n1 | cut -c1-200)"
    done < <(printf '%s' "$runs" | jq -r --arg h "$h" '.[] | select(.bad == 1 and .head == $h) | [.rule, .kind, (.run_at | tostring), .why] | @tsv')
  done <<<"$heads_bad"

  # (2) a commit this session recorded that no rule has passed yet: every file-guard that ran
  # anywhere in the family needs a finished passing run at the commit or above it.
  recorded="$(owed_recorded)"
  [ -n "$recorded" ] || return 0
  rules="$(printf '%s' "$runs" | jq -r '.[] | select(.rule | contains("file-guard/")) | .rule' | sort -u)"
  for t in ${tips[@]+"${tips[@]}"}; do
    case "$t" in *[!0-9a-f]* | "") continue ;; esac
    hit=""
    while read -r rname rtip rab; do
      [ -n "$rtip" ] || continue
      [ "$rab" = "$rtip" ] && continue # the user had it dropped, at this tip
      if [ "$rtip" = "$t" ]; then hit=1; break; fi
      for b in ${branches[@]+"${branches[@]}"}; do
        [ -n "$b" ] && [ "$rname" = "refs/heads/$b" ] && hit=1
      done
      [ -n "$hit" ] && break
      case "$rtip" in *[!0-9a-f]*) continue ;; esac
      if ! in_default "$rtip" && git -C "$ws" merge-base --is-ancestor "$rtip" "$t" 2>/dev/null; then hit=1; break; fi
    done <<<"$recorded"
    [ -n "$hit" ] || continue
    missing=""
    if [ -z "$rules" ]; then
      missing="  - no file-guard has judged anything in this session yet"
    else
      while IFS= read -r r; do
        [ -n "$r" ] || continue
        ok=""
        while IFS= read -r g; do
          [ -n "$g" ] || continue
          if git -C "$ws" merge-base --is-ancestor "$t" "$g" 2>/dev/null; then ok=1; break; fi
        done < <(printf '%s' "$runs" | jq -r --arg r "$r" '.[] | select(.rule == $r and .good == 1) | .head')
        [ -n "$ok" ] || missing="${missing:+$missing$'\n'}  - $r"
      done <<<"$rules"
    fi
    [ -n "$missing" ] && owed_listing="${owed_listing:+$owed_listing$'\n'}${t:0:12}, not yet passed by:"$'\n'"$missing"
  done
  return 0
}

# Everything at stake at once: each recorded tip (abandoned ones excepted) and each refused
# head, as tips[] and branches[].
owed_all() {
  tips=()
  branches=()
  local rname rtip rab h
  while read -r rname rtip rab; do
    [ -n "$rtip" ] || continue
    [ "$rab" = "$rtip" ] && continue
    in_default "$rtip" && continue # landed: nothing to lose
    tips+=("$rtip")
    case "$rname" in refs/heads/*) branches+=("${rname#refs/heads/}") ;; esac
  done < <(owed_recorded)
  while IFS= read -r h; do
    [ -n "$h" ] || continue
    in_default "$h" && continue
    tips+=("$h")
  done < <(printf '%s' "$runs" | jq -r '.[] | select(.bad == 1) | .head' | sort -u)
  owed_unique
  return 0
}
