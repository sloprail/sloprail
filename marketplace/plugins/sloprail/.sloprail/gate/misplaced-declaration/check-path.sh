#!/bin/sh
# The gate half of misplaced-declaration: the rule reads only the event path, so the
# check is the file-guard's, run unchanged before the write lands.
exec "$(dirname "$0")/../../file-guard/misplaced-declaration/check-path.sh"
