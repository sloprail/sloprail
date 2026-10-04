set -e
unset rule_tests_cases_loaded
. "$(dirname "$0")/../_lib.sh" || exit 2
[ "${rule_tests_cases_loaded:-}" = 1 ] || exit 2
write_no_curl
write_no_curl_tests
git add -A
git commit -q -m "the no-curl gate with its cases"
git tag base
# the change: the gate stops matching curl, and nobody touched its cases
sed -i.bak 's/"curl"/"wget"/' .sloprail/gate/no-curl/gate.yaml
rm .sloprail/gate/no-curl/gate.yaml.bak
git add -A
git commit -q -m "no-curl: match wget instead"
