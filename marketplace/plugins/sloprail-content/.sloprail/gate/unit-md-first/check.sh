#!/usr/bin/env bash
# Gate copy: refuse a PENDING write inside a unit folder whose UNIT.md does not
# exist yet. Path-based, not content-based, so an unresolvable pre-write result
# (resultKnown false) is still judged — nothing here reads newContent.
lib_dir="$(cd "$(dirname "$0")/../../file-guard/unit-md-first" && pwd)"
unset check_lib_loaded
. "$lib_dir/check-lib.sh" || exit 2
[ "${check_lib_loaded:-}" = 1 ] || exit 2
lib_init
case "$kind" in
  PreFileCreate | PreFileUpdate) ;;
  *) refuse "unexpected event kind '$kind' for $path; this gate only judges pending file writes" ;;
esac
lib_check
