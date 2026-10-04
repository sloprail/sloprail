set -e
. "$SR_TEST_CASE_DIR/../_lib.sh"
write_no_curl
write_no_curl_tests
git add -A
git commit -q -m "the no-curl gate with its cases"
git tag base
rm -r .sloprail/gate/no-curl/tests
git add -A
git commit -q -m "no-curl: drop the cases"
