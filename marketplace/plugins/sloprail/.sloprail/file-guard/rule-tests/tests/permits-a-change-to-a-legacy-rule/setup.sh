set -e
. "$SR_TEST_CASE_DIR/../_lib.sh"
write_no_curl
git add -A
git commit -q -m "the no-curl gate, written before tests existed"
git tag base
echo "# the reason is clearer now" >>.sloprail/gate/no-curl/refuse.sh
git add -A
git commit -q -m "no-curl: a comment"
