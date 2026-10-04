set -e
echo "a project" >README.md
git add -A
git commit -q -m "the project"
git tag base
mkdir -p .sloprail/file-guard .sloprail/gate/no-curl/tests/refuses-curl
printf 'allow: [src/**]\n' >.sloprail/file-guard/structure.yaml
printf 'on: []\n' >.sloprail/gate/no-curl/gate.yaml
printf 'expect: refuse\n' >.sloprail/gate/no-curl/tests/refuses-curl/case.yaml
printf 'stop_hook_block_cap: 4\n' >.sloprail/config.yaml
git add -A
git commit -q -m "declarations in place"
