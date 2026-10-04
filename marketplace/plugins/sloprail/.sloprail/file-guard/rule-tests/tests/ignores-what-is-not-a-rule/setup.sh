set -e
. "$SR_TEST_CASE_DIR/../_lib.sh"
base_commit
printf 'stop_hook_block_cap: 4\n' >.sloprail/config.yaml
git add -A
git commit -q -m "tune the project"
