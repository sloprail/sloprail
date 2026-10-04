set -e
. "$SR_TEST_CASE_DIR/../_lib.sh"
base_commit
write_no_curl
write_no_curl_tests
git add -A
git commit -q -m "add the no-curl gate with its cases"
