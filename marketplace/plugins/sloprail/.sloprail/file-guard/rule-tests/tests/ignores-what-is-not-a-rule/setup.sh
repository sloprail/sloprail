set -e
unset rule_tests_cases_loaded
. "$(dirname "$0")/../_lib.sh" || exit 2
[ "${rule_tests_cases_loaded:-}" = 1 ] || exit 2
base_commit
printf 'stop_hook_block_cap: 4\n' >.sloprail/config.yaml
git add -A
git commit -q -m "tune the project"
