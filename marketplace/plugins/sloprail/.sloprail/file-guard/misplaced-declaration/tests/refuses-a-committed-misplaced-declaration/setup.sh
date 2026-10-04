set -e
echo "a project" >README.md
git add -A
git commit -q -m "the project"
git tag base
printf 'allow: [src/**]\n' >.sloprail/structure.yaml
git add -A
git commit -q -m "add a structure"
