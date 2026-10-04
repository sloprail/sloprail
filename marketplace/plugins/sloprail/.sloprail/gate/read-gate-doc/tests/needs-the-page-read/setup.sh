set -e
# the skill the gate points at, in the project's own .claude/skills
mkdir -p .claude/skills/authoring-guardrails
printf '%s\n' '---' 'name: authoring-guardrails' 'description: how to write a guardrail' '---' '# Authoring' >.claude/skills/authoring-guardrails/SKILL.md
echo "# Gate" >.claude/skills/authoring-guardrails/gate.md
git add -A
git commit -q -m "the project, with the authoring skill"
