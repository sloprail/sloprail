set -e
unset rule_tests_cases_loaded
. "$(dirname "$0")/../_lib.sh" || exit 2
[ "${rule_tests_cases_loaded:-}" = 1 ] || exit 2
base_commit
write_no_curl
git add -A
git commit -q -m "add the no-curl gate"
