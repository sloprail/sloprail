set -e
. "$SR_TEST_CASE_DIR/../_lib.sh"
base_commit
write_no_curl
git add -A
git commit -q -m "add the no-curl gate"
