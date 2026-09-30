#!/usr/bin/env bash
# Shared by the skill-quality gate and its file-guard: the ONE deterministic size cap, so the
# two can never disagree about how large is too large. Sourced, never executed; it
# reads no event and never exits — size_cap_refuses returns 0 when the bytes are over
# the cap (and prints the refusal on stderr), 1 when they fit.
#
# Every file plus every meta-rule goes into one judge prompt, so past roughly
# max_bytes the request exceeds the model's context and no verdict comes back. Size is
# caused by the content, identical on every run, and fixable, so it is refused and
# named BEFORE a model is asked, rather than left to a generic model failure.
# Threshold measured on the sibling judges: 534KB judged fine, 929KB rejected by the
# model.
size_cap_max_bytes=600000

# size_cap_refuses BYTES SUBJECT — SUBJECT reads "the skill files in this changeset"
# (file-guard) or the file path (gate).
size_cap_refuses() {
  local bytes="$1" subject="$2"
  [ "$bytes" -gt "$size_cap_max_bytes" ] || return 1
  cat >&2 <<SR_EOF
SKILL QUALITY: $subject: ${bytes} bytes, which is too
large to judge (they all go into the judge's prompt, and past roughly ${size_cap_max_bytes} bytes
the request exceeds the model's context and no verdict comes back).

Refused rather than permitted because the size is itself the finding. Split the
work into smaller files, or cut the skill down, and it will be judged normally.
SR_EOF
  return 0
}
