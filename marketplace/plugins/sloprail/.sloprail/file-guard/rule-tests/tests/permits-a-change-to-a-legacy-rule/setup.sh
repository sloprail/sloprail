set -e
unset rule_tests_cases_loaded
. "$(dirname "$0")/../_lib.sh" || exit 2
[ "${rule_tests_cases_loaded:-}" = 1 ] || exit 2
write_no_curl
git add -A
git commit -q -m "the no-curl gate, written before tests existed"
git tag base
echo "# the reason is clearer now" >>.sloprail/gate/no-curl/refuse.sh
git add -A
git commit -q -m "no-curl: a comment"
