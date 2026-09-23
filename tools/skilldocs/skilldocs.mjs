#!/usr/bin/env node
/**
 * Source the "Guides" (authoring how-to) from the authoring-guardrails skill.
 *
 * The skill the plugin ships IS the authoritative guidance for writing a
 * guardrail — natures, matchers, checks, state, environment. Rather than
 * re-writing (and drifting from) it, the docs render it: this copies the skill's
 * markdown into docs/guides/, adding doc frontmatter (title from the
 * H1, kind, related) and rewriting the skill-relative `[x.md](x.md)` links to
 * doc routes. Re-run whenever the skill changes; the output is generated, not
 * hand-edited.
 *
 *   node tools/skilldocs/skilldocs.mjs
 */
import { readFileSync, writeFileSync, mkdirSync, readdirSync, rmSync } from 'node:fs';
import { join, basename } from 'node:path';

const SKILL_DIR = 'marketplace/plugins/sloprail/skills/authoring-guardrails';
const OUT_DIR = 'docs/guides';
const ROUTE = '/guides';

// Order + friendly labels for the pages (SKILL is the overview/index).
const ORDER = [
  'SKILL', 'file-guard', 'gate', 'context', 'structure-gate', 'matchers',
  'script-checks', 'judge-checks', 'events', 'state-management', 'environment',
];

// A short related set per page — points at the concept idea + siblings.
const RELATED = {
  'file-guard': ['concepts/file-guard', 'guides/matchers', 'guides/script-checks'],
  'gate': ['concepts/gate', 'guides/matchers', 'concepts/precondition'],
  'context': ['concepts/context', 'guides/state-management'],
  'matchers': ['guides/events', 'guides/file-guard'],
  'script-checks': ['guides/judge-checks', 'concepts/refusal-contract'],
  'judge-checks': ['guides/script-checks', 'concepts/grounding'],
  'events': ['reference/event-vocabulary', 'guides/matchers'],
  'state-management': ['guides/context'],
  'environment': ['guides/script-checks'],
};

function h1(src) {
  const m = src.match(/^#\s+(.+)$/m);
  return m ? m[1].trim() : 'Authoring';
}

// Strip an existing skill frontmatter block if present (SKILL.md has one).
function stripFrontmatter(src) {
  return src.replace(/^---\n[\s\S]*?\n---\n/, '');
}

// Remove the leading H1 (it becomes the page title) so it isn't shown twice.
function stripH1(src) {
  return src.replace(/^#\s+.+\n+/, '');
}

// Nice title for a page slug, for link text ("script-checks" -> "Script checks").
function niceLabel(name) {
  const s = name.replace(/-/g, ' ');
  return s.charAt(0).toUpperCase() + s.slice(1);
}

// Rewrite skill-relative links to doc routes, fixing BOTH the href and the link
// text — the skill often uses the bare filename as the text ([matchers.md](matchers.md)),
// which would otherwise render a literal ".md". SKILL.md maps to the section index.
function rewriteLinks(src) {
  return src.replace(/\[([^\]]+)\]\(([a-z-]+)\.md(#[a-z0-9-]+)?\)/gi, (_, text, name, hash) => {
    const slug = name === 'SKILL' ? '' : `/${name}`;
    // If the link text was just the filename, replace it with a readable label.
    const cleanText = /^[a-z-]+\.md$/i.test(text)
      ? (name === 'SKILL' ? 'Authoring guardrails' : niceLabel(name))
      : text;
    return `[${cleanText}](${ROUTE}${slug}${hash || ''})`;
  });
}

// Minimal frontmatter — just enough for routing and the sidebar. The body is
// the skill's own prose, untouched beyond link rewriting. No injected
// descriptions or related lists: this is the skill, verbatim, as docs.
function frontmatter(name, title) {
  const isIndex = name === 'SKILL';
  const order = ORDER.indexOf(name);
  const lines = ['---', `title: ${title.replace(/"/g, '\\"')}`];
  lines.push(`kind: ${isIndex ? 'explanation' : 'reference'}`);
  lines.push('sidebar:', `  order: ${order < 0 ? 99 : order}`);
  lines.push('---', '', '<!-- Sourced verbatim from the authoring-guardrails skill by tools/skilldocs — do not hand-edit. -->', '');
  return lines.join('\n');
}

function main() {
  rmSync(OUT_DIR, { recursive: true, force: true });
  mkdirSync(OUT_DIR, { recursive: true });

  const files = readdirSync(SKILL_DIR).filter((f) => f.endsWith('.md'));
  let n = 0;
  for (const f of files) {
    const name = basename(f, '.md');
    const raw = readFileSync(join(SKILL_DIR, f), 'utf8');
    const title = name === 'SKILL' ? 'Authoring guardrails' : h1(raw);
    let body = stripFrontmatter(raw);
    body = stripH1(body);
    body = rewriteLinks(body);
    const outName = name === 'SKILL' ? 'index.md' : `${name}.md`;
    writeFileSync(join(OUT_DIR, outName), frontmatter(name, title) + body);
    n++;
  }
  console.error(`[skilldocs] wrote ${n} pages to ${OUT_DIR}`);
}

main();
