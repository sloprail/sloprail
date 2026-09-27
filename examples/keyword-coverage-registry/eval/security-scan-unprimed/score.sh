#!/bin/sh
# security-scan's scorer, told there is no skill: the refusals' remedies are the
# only teacher here, and the judge must read a refusal-then-declare path as the
# expected one.
SCAN_PRIMED=no
export SCAN_PRIMED
exec sh "$(dirname "$0")/../security-scan/score.sh"
