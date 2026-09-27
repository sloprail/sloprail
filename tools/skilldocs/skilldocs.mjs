#!/usr/bin/env node
/**
 * Source the "Guides" (authoring how-to) from the authoring-guardrails skill.
 *
 * The skill the plugin ships IS the authoritative guidance for writing a
 * guardrail — natures, matchers, checks, state, environment. Rather than
 * re-writing (and drifting from) it, the docs render it: this copies the skill's
 * markdown into docs/guides/, adding doc frontmatter (title from the
 * H1, kind, related) and rewriting the skill-relative `[x.md](x.md)` links to
 * doc routes.
 *
 * The output is NOT committed. The skill is the only copy in this repo; the docs
 * site (the website repo's scripts/pull-docs.mjs) runs this at build time against
 * the pinned sloprail commit, so the rendered guides can never lag the skill.
 * docs/guides/ is gitignored here; run it locally to preview:
 *
 *   node tools/skilldocs/skilldocs.mjs [out-dir]     # default: docs/guides
 *
 * Paths into the skill are resolved from this script's own location, so it can
 * be run from any directory — the site calls it from outside this checkout and
 * passes its own content folder as out-dir.
 */
import { readFileSync, writeFileSync, mkdirSync, readdirSync, rmSync } from 'node:fs';
import { join, basename, dirname, resolve, relative } from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
const PLUGIN_DIR = join(REPO_ROOT, 'marketplace/plugins/sloprail');
const SKILL_NAME = 'authoring-guardrails';
const SKILL_DIR = join(PLUGIN_DIR, 'skills', SKILL_NAME);
const OUT_DIR = resolve(process.argv[2] || join(REPO_ROOT, 'docs/guides'));
const ROUTE = '/guides';

// The commit this checkout is actually at, for a stable (never-moving)
// GitHub blob URL — pull-docs.mjs runs this with cwd set to a real clone of
// sloprail pinned at docs-source.json's ref, so `git rev-parse HEAD` there
// answers exactly that ref, not a moving branch. Falls back to `main` for a
// local preview run (this script's own docstring: `node tools/skilldocs/
// skilldocs.mjs` from an ordinary working copy, possibly with uncommitted
// changes HEAD can't speak for anyway).
function resolveCommit() {
  try {
    return execFileSync('git', ['rev-parse', 'HEAD'], { cwd: REPO_ROOT, encoding: 'utf8' }).trim();
  } catch {
    return 'main';
  }
}
const COMMIT = resolveCommit();
const GITHUB_BLOB_BASE = `https://github.com/sloprail/sloprail/blob/${COMMIT}`;

// The user-facing invocation is `/<plugin>:<skill>` — plugin name from the
// plugin manifest, skill from its directory. Read them so the command in the
// docs can't drift from what the plugin is actually called.
const pluginName = JSON.parse(
  readFileSync(join(PLUGIN_DIR, '.claude-plugin', 'plugin.json'), 'utf8'),
).name;
const SKILL_COMMAND = `/${pluginName}:${SKILL_NAME}`;

// Order + friendly labels for the pages (SKILL is the overview/index).
const ORDER = [
  'SKILL', 'file-guard', 'gate', 'context', 'structure-gate', 'matchers',
  'script-checks', 'judge-checks', 'events', 'state-management', 'environment',
];

// A short related set per page — points at its siblings.
const RELATED = {
  'file-guard': ['guides/matchers', 'guides/script-checks'],
  'gate': ['guides/matchers', 'guides/script-checks'],
  'context': ['guides/state-management'],
  'matchers': ['guides/events', 'guides/file-guard'],
  'script-checks': ['guides/judge-checks'],
  'judge-checks': ['guides/script-checks'],
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
// Tolerates leading blank lines left after the frontmatter strip.
function stripH1(src) {
  return src.replace(/^\s*#\s+.+\n+/, '');
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
  src = src.replace(/\[([^\]]+)\]\(([a-z-]+)\.md(#[a-z0-9-]+)?\)/gi, (_, text, name, hash) => {
    const slug = name === 'SKILL' ? '' : `/${name}`;
    // If the link text was just the filename, replace it with a readable label.
    const cleanText = /^[a-z-]+\.md$/i.test(text)
      ? (name === 'SKILL' ? 'Authoring guardrails' : niceLabel(name))
      : text;
    return `[${cleanText}](${ROUTE}${slug}${hash || ''})`;
  });
  // A sibling that is NOT a rendered doc page — check-template.sh, say — has no
  // route of its own; the generated site never copies it alongside the page.
  // Point it at the file's actual GitHub source instead of leaving a relative
  // link that 404s the moment this prose is republished somewhere that isn't
  // a checkout of the skill directory. Text is left as the reader wrote it
  // ("check-template.sh" already reads fine, unlike a bare ".md" filename).
  return src.replace(/\[([^\]]+)\]\(([a-z0-9_-]+\.[a-z0-9]+)\)/gi, (_, text, filename) => {
    const relPath = relative(REPO_ROOT, join(SKILL_DIR, filename));
    return `[${text}](${GITHUB_BLOB_BASE}/${relPath})`;
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
  lines.push('---', '');
  // A generated-file note. For the index (MDX with an import right after) use an
  // MDX comment so it doesn't sit awkwardly before the import; plain md gets an
  // HTML comment.
  lines.push(isIndex
    ? '{/* Sourced from the authoring-guardrails skill by tools/skilldocs — do not hand-edit. */}'
    : '<!-- Sourced verbatim from the authoring-guardrails skill by tools/skilldocs — do not hand-edit. -->');
  lines.push('');
  return lines.join('\n');
}

// The command block that leads the index — a user runs the skill, they don't
// read it all. `/<plugin>:<skill>` is the plugin-namespaced skill
// invocation (plugin name from plugin.json + the skill's own name). Injected by
// the generator so it survives regeneration. Only Claude Code is live, in a tab
// like the install page.
const INDEX_INTRO = `import { Tabs, TabItem } from '@astrojs/starlight/components';

You don't have to read this to author a guardrail — run the skill and describe
what you want; it walks the rest.

<Tabs>
  <TabItem label="Claude Code">
    \`\`\`bash
    ${SKILL_COMMAND} add a rule that <what you want to enforce>
    \`\`\`
  </TabItem>
</Tabs>

More harnesses get their own tab as they go live. The rest of this page is the
same guidance the skill follows, if you'd rather read it.

`;

function main() {
  rmSync(OUT_DIR, { recursive: true, force: true });
  mkdirSync(OUT_DIR, { recursive: true });

  const files = readdirSync(SKILL_DIR).filter((f) => f.endsWith('.md'));
  let n = 0;
  for (const f of files) {
    const name = basename(f, '.md');
    const isIndex = name === 'SKILL';
    const raw = readFileSync(join(SKILL_DIR, f), 'utf8');
    const title = isIndex ? 'Authoring guardrails' : h1(raw);
    let body = stripFrontmatter(raw);
    body = stripH1(body);
    body = rewriteLinks(body);
    // The index leads with the run-the-skill command block; it needs MDX for the
    // Tabs component, so it is the one page written as .mdx.
    const intro = isIndex ? INDEX_INTRO : '';
    const outName = isIndex ? 'index.mdx' : `${name}.md`;
    writeFileSync(join(OUT_DIR, outName), frontmatter(name, title) + intro + body);
    n++;
  }
  console.error(`[skilldocs] wrote ${n} pages to ${OUT_DIR}`);
}

main();
