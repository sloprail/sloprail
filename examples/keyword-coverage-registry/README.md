# keyword-coverage-registry (context + gates + file-guard)

## The rule

GitHub research happens **against a declared scanner, through `gh`**. Before
searching GitHub for a topic, the agent declares a scanner —
`scanners/<name>/scanner.yaml` naming every keyword the topic requires:

```yaml
active: true
keywords:
  - auth token
  - logging
  - leak
```

— and then ONE `gh` call must carry all of those keywords together. A search
split across several narrower calls does not count, a scanner cannot be
weakened (or deleted) to fit the search already run, and research done any
other way — WebSearch, fetching GitHub pages, a `gh search` with no scanner
declared — is refused, because the registry cannot see it.

It exists because of what real runs did. Given a skill teaching the convention,
Haiku researched through WebSearch in 4 of 5 runs and never declared a scanner;
once it did declare one, it rewrote the keywords to match a search it had
already run; and a sub-agent refused for coverage ran `rm -rf scanners/<name>`
instead of searching — after which nothing refused it again.

## The five rules

| rule | nature | fires on | refuses |
|---|---|---|---|
| `scanner-declared` | context | `PreFileWrite`, `PostFileCreate`, `PostFileUpdate` of `**/scanners/<name>/scanner.yaml` | nothing — it logs the scanner's keywords into its registry |
| `github-research-through-gh` | gate | `PreToolUse`: any `WebSearch`; a `WebFetch` of a GitHub content host. `PreCommandInvoke`: `curl`/`wget`/httpie of one | GitHub research outside `gh` |
| `search-needs-declared-scanner` | gate | `PreCommandInvoke`: a `gh search …` / `gh api search/…` / `gh api graphql … search(…)` | a search with no scanner declared this session |
| `verify-scanner-coverage` | gate | `Stop`, while `scanner-declared` is active | a declared scanner no single gh call covered |
| `scanner-keywords-hold` | file-guard, preventive, `deletions: include` | the scanner file | dropping a keyword, or deleting the scanner, without the user's words |

## Why a context and gates

The rule has two halves that happen at different moments. "A scanner was
declared" is a **mode** that other rules depend on — so it is a context,
`scanner-declared`, which logs each scanner's full keyword set into its
`sr-session state` registry (`scanner:<name>`). "Was it covered" and "may this
search run" are **checkpoints** — so they are gates, and they read that
registry with the cross-guardrail `state list --owner scanner-declared` read.

The coverage gate reads the registry rather than deriving the obligation from
the searches themselves, so a search that never ran is caught missing. It is a
script, not a judge: the scanner names its own keywords, so "does one gh call
contain every one of them" (case-insensitive, whole-word) is a structural fact,
decided free and reproducibly. Its refusal spells the covering search out —
each uncovered scanner's keywords, and that the one call counts even if GitHub
returns nothing for so specific a query: a real sub-agent whose covering search
came back empty read the refusal as "get results with all keywords", tried to
drop keywords (refused) and thrashed through twenty narrower searches, although
its covering call had already satisfied the gate.

### When the context logs a scanner

Twice, because its two readers need it at different moments:

- **At the `PreFileWrite`** of the scanner file, so a `gh search` later in the
  **same turn** finds it declared. Post events are built from the tree diff at
  Stop only — a Post-only context is still empty when that search runs, and
  `search-needs-declared-scanner` then refused every search that followed a
  declaration. A write may still be refused after a context enters (contexts
  enter before preventive file-guards), so at Pre the entry only ever grows: it
  becomes the union of what was logged and what the write declares.
- **At the `PostFile*`** (Stop), from the settled file — including one written
  by a shell command, whose bytes a Pre event cannot know. Here the entry
  becomes exactly the file's keywords; anything that dropped one already got
  past `scanner-keywords-hold`.

An entry is **never removed**. A scanner declared this session stays owed its
search even if its file is later switched off or deleted.

### Why the context stays open while coverage is refused

`verify-scanner-coverage` runs only while `scanner-declared` is active, and the
context is re-entered only by a Post event for a scanner file that still
differs from the session's baseline. Its `exit` used to close it at every Stop.
So when the file vanished — the sub-agent's `rm -rf` — the context closed at
the refused Stop, nothing re-entered it, and the gate never ran again. The exit
now mirrors the gate's verdict: it closes only once `verify-scanner-coverage`
passed, so a refusal keeps standing, from the registry, whether or not the file
survives.

## Why the research gates

`verify-scanner-coverage` holds a search to a declared scanner — but only a
`gh` search, only at Stop, and only once a scanner exists. Everything else
escaped: research through WebSearch or WebFetch is not a gh call, and a `gh
search` before any declaration had nothing to be held to. Two pre-action gates
close that, each refusing with the remedy — declare the scanner, then cover it
in one `gh search`:

- **`github-research-through-gh`** (`PreToolUse`, matched on `event.tool`).
  **WebSearch is refused outright** in this project — see the tradeoff below.
  **WebFetch is refused only for the hosts that serve repositories, issues,
  code and gists**: `github.com`, `www.`, `api.`, `gist.` and `codeload.github.com`,
  and any `*.githubusercontent.com` (raw, gist, objects). GitHub's documentation
  and project sites (`docs.github.com`, `github.blog`, `*.github.io`) are not
  what a scanner searches and stay fetchable, as does every other URL. The host
  is matched anchored at the URL's start, case-insensitive, with an optional
  scheme, userinfo and port — so `https://example.com/?u=github.com` and
  `github.com.evil.example` are not GitHub. **The same hosts fetched from the
  shell** — `curl`, `wget`, httpie/`xh`, any argument anchored as such a URL —
  are refused too: once WebSearch was refused, a real run read issues with
  `curl https://api.github.com/repos/…` and files from `raw.githubusercontent.com`,
  and a curl of `api.github.com/search` is a search no scanner governs. The
  remedy names the gh equivalents (`gh issue view`, `gh api repos/…/contents/…`).
- **`search-needs-declared-scanner`** (`PreCommandInvoke`). What counts as a
  search is decided on the engine's **parsed invocations** (`event.invocations`),
  never a regex over the raw line — so `cd x && gh search …`, `FOO=1 gh search …`,
  `env … gh …`, `command gh …`, `bash -c "gh search …"`, `xargs gh search …`, a
  full `/usr/local/bin/gh` path and quoted arguments are all the same `gh`
  invocation. A search is `argv[1] == "search"`, or `gh api` naming a
  `search/…` endpoint (path or full URL) or a GraphQL `search(` field. Every
  other gh call — `gh issue view`, `gh repo view`, `gh api repos/…` — reads a
  thing already found and is left alone, as is the scanner write itself.

  Its check reads the **registry**, not `context["scanner-declared"].active` or
  a `require: context`: activity is per cycle (the context closes once coverage
  passed), so a scanner declared in an earlier turn would read as undeclared;
  and `require`'s refusal is the engine's generic wording, which names no
  remedy. Plumbing fails open (a registry that cannot be read permits, said on
  stderr); the logic fails closed.

  The registry is per session, and a sub-agent may be judged in its own
  session. The remedy says what to do then: a scanner that already exists is
  registered by writing it again, unchanged.

### The WebSearch tradeoff

Refusing WebSearch outright costs this project web search for everything, not
only GitHub. That is deliberate for a **GitHub-research registry**: a query
cannot be told apart by its words ("auth token leak logs" is a GitHub search in
all but name), its results are largely GitHub pages anyway, and a partial rule
("refuse queries mentioning github") is one an agent routes around by
rephrasing. A project that needs general web search alongside this registry
should narrow the WebSearch trigger with `event.input.query` or
`event.input.allowed_domains`, and accept that research can leak through it.

## Why `scanner-keywords-hold` is a preventive file-guard

It judges what the scanner **file** holds — its keyword set may grow but not
shrink — so it is a file-guard, `preventive` so a weakened declaration is
refused before it lands, while the agent can still meet it with a search. The
citation requirement is conditional: `drops-keywords.sh` (a `when`) applies it
only when the write drops a declared keyword; an uncited drop is refused with
its hint, a cited one goes to a judge that checks the cited words ask for
THESE keywords to go.

`deletions: include`, because deleting the scanner drops every keyword at once.
`rm -rf scanners/<name>` — the directory, as the real run did it — reaches the
guard as a `PreFileDelete` of the scanner file inside: the engine expands a
recursive removal of a directory (`rm -r`/`-R`, or `mv` of it) into one delete
per file it holds. A delete the engine cannot see (`find … -delete`, a script)
still does not clear the obligation — the registry keeps the scanner and the
context stays open.

## What it does not catch

- **Paraphrase.** Coverage is literal keywords; a search for a synonym does not
  count, by design — the scanner names its own words.
- **Research through another channel** the gates do not know: an MCP GitHub
  server, a script that fetches for the agent, a fetching program not in the
  list. Add a trigger for it if a project has one.
- **Parsing is a correctness aid, not a security boundary**: a program named by
  a variable (`$GH search …`) or a decoded payload is not visible to
  `event.invocations`.

## Proof

- e2e: `tests/e2e/examples/038_keyword_coverage_registry/` — the context and
  registry (T038_01–03), coverage (T038_04–07), keywords-hold (T038_08–12), the
  research gates incl. every wrapped `gh` form (T038_13–20), scanner deletion
  and the obligation surviving an unseen delete (T038_21–24), shell fetches of
  GitHub (T038_25).
- eval: `eval/security-scan/` — a real Haiku run with its full toolset
  (WebSearch and WebFetch included), scored on trajectory health, with a
  deterministic failure for a declared scanner deleted before the run ended.
