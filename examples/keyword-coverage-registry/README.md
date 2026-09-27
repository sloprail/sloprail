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
| `search-needs-declared-scanner` | gate | `PreCommandInvoke`: any `gh` call | a gh call that is not a known read of something already found, with no scanner declared this session |
| `verify-scanner-coverage` | gate | `Stop`, while `scanner-declared` is active | a declared scanner no single gh call covered |
| `scanner-keywords-hold` | file-guard, preventive, `deletions: include` | the scanner file | dropping a keyword, or deleting the scanner, without the user's words |

## Why a context and gates

The rule has two halves that happen at different moments. "A scanner was
declared" is a **mode** that other rules depend on — so it is a context,
`scanner-declared`, which logs each scanner's full keyword set into its
`sr-session state` registry, keyed by the scanner's folder
(`scanner:scanners/mine`). "Was it covered" and "may this search run" are
**checkpoints** — so they are gates, and they read that registry with the
cross-guardrail `state list --owner scanner-declared` read.

The key is the whole workspace-relative folder, not its last name: keyed by
name, `scanners/mine` and `zz/scanners/mine` were one entry, and a second
scanner — created fresh, so nothing asked for a citation — replaced the first
one's keywords with a narrower set a search could then cover.

Every rule reads a scanner file and the registry through one shared file,
`context/scanner-declared/scanner-lib.sh`. The keyword-hold guard used to parse
keywords its own way: it stopped at a column-0 comment the registry read past
(so a keyword after one could be dropped with no citation), and it did not strip
quotes (so re-quoting a keyword read as dropping it). One parser cannot
disagree with itself.

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
  becomes the union of what was logged, what the file on disk declares now, and
  what the write declares. The file on disk matters for a committed scanner
  this session never logged: a refused write narrowing it is followed by no
  Post event (the file never changed), so a union with the log alone registered
  the narrowed set.
- **At the `PostFile*`** (Stop), from the settled file — including one written
  by a shell command, whose bytes a Pre event cannot know. Here the entry
  becomes exactly the file's keywords; anything that dropped one already got
  past `scanner-keywords-hold`.

An entry is **never removed**. A scanner declared this session stays owed its
search even if its file is later switched off, or deleted by a route no rule
saw. The one exception is a delete the user asked for: see
[retiring a scanner](#retiring-a-scanner-the-user-deleted).

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
  code and gists**: `github.com`, `www.`, `api.`, `gist.`, `codeload.`, `raw.`
  and `uploads.github.com`, and any `*.githubusercontent.com` (raw, gist,
  objects). GitHub's documentation and project sites (`docs.github.com`,
  `github.blog`, `*.github.io`) are not what a scanner searches and stay
  fetchable, as does every other URL. The host is matched anchored at the URL's
  start, case-insensitive, with an optional scheme, userinfo, trailing dot
  (`github.com.` is the same host) and port — so
  `https://example.com/?u=github.com` and `github.com.evil.example` are not
  GitHub. **The same hosts fetched from the shell** — `curl`, `wget`,
  httpie/`xh`, any argument anchored as such a URL — are refused too: once
  WebSearch was refused, a real run read issues with
  `curl https://api.github.com/repos/…` and files from `raw.githubusercontent.com`,
  and a curl of `api.github.com/search` is a search no scanner governs. A URL
  built from a variable (`U=https://api.github.com; curl $U/search/issues`)
  never reaches the parsed arguments — the parser drops what it cannot
  resolve — so a fetch is also refused on a line that expands something and
  puts a GitHub host into a variable (`NAME=…github.com…`) or a `$(…)`/`${…}`.
  The remedy names the gh equivalents (`gh issue view`, `gh api repos/…/contents/…`).
- **`search-needs-declared-scanner`** (`PreCommandInvoke`, every `gh` call).
  Until a scanner is declared, a gh call runs only if it is a **known read** of
  something already found: `gh --version`/`help`/`auth status`;
  `gh issue|pr|repo|release|gist view`; the same `list` commands without
  `--search`/`-S`; `gh api <endpoint>` where the endpoint is found and is neither
  `graphql` nor a `search/…` path. **Everything else counts as a search.** The
  rule used to list search spellings instead (`gh search`, `gh api search/…`, a
  GraphQL `search(`), and the list was measured short: `gh issue list --search`,
  `-S`, `search (` with a space, a GraphQL query read from a file
  (`-F query=@q.graphql`), a gh alias, and `X=search; gh $X …` (the parser drops
  the unresolvable word, leaving `gh issues …`) all ran with no scanner. It is
  decided on the engine's **parsed invocations** (`event.invocations`), so
  `cd x && gh …`, `FOO=1 gh …`, `env …`, `command gh …`, `bash -c "gh …"`,
  `xargs gh …`, a full `/usr/local/bin/gh` path and quoted arguments are all the
  same `gh` invocation. The scanner write itself is never refused.

  Its check reads the **registry**, not `context["scanner-declared"].active` or
  a `require: context`: activity is per cycle (the context closes once coverage
  passed), so a scanner declared in an earlier turn would read as undeclared;
  and `require`'s refusal is the engine's generic wording, which names no
  remedy. **Both the logic and the plumbing fail closed**: a registry that
  cannot be read, or does not parse, refuses the gh call — and
  `verify-scanner-coverage` refuses the Stop — naming the plumbing failure. (Both
  used to fail open, each citing the other as the backstop; the coverage gate's
  read piped a failed `sr-session` into `jq -s`, which reads nothing as `[]`, so
  it passed.)

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
THESE keywords to go. The judge's `prepare` skips the model only on
`drops-keywords.sh`'s decided "drops nothing" (exit 1): a predicate that could
not run at all — not executable, missing, crashed — used to read as "drops
nothing" too, skip the judge, and let any quote of the user's admit the drop.

`deletions: include`, because deleting the scanner drops every keyword at once.
`rm -rf scanners/<name>` — the directory, as the real run did it — reaches the
guard as a `PreFileDelete` of the scanner file inside: the engine expands a
recursive removal of a directory (`rm -r`/`-R`/`--recursive` or an
abbreviation of it, or `mv` of it) into one delete per file it holds. A delete
the engine cannot see (`find … -delete`, a script, a directory too large to
expand — see below) still does not clear the obligation — the registry keeps
the scanner and the context stays open.

### Retiring a scanner the user deleted

A delete the user asked for — cited, and judged to be what their words ask —
**retires** the scanner's obligation. Without that, a scanner declared this
session stayed in the registry forever, and every later Stop was refused,
telling the agent to search for a scanner the user had removed.

The guard's last check, `retire-on-delete.sh`, runs only once the checks before
it admitted the event, and records `retired:<folder>` at the declaration's
current stamp (`stamp:<folder>`, which `scanner-declared` renews at every
declaration). The registry's reader counts a scanner as retired only while that
stamp still matches — declaring it again makes it owed again — and while its
file is really gone, so a delete some other rule refused leaves it owed. A
delete no rule saw never reaches the check and retires nothing.

## What it does not catch

- **Paraphrase.** Coverage is literal keywords; a search for a synonym does not
  count, by design — the scanner names its own words.
- **Research through another channel** the gates do not know: an MCP GitHub
  server, a script that fetches for the agent, a fetching program not in the
  list. Add a trigger for it if a project has one.
- **Parsing is a correctness aid, not a security boundary**: a program named by
  a variable (`$GH search …`) or a decoded payload is not visible to
  `event.invocations`. A URL built from a variable is caught only when the same
  line puts the GitHub host into a variable or an expansion; one set in an
  earlier command (`export API=https://api.github.com`, then `curl $API/…`) is
  not.
- **A directory too large to expand.** The engine predicts a recursive
  removal's deletes only up to 1000 files and 8 MiB; past either bound it
  predicts none, so padding a scanner's folder hides its `rm -rf` from this
  guard. The registry still holds the scanner, so it stays owed its search.

## Proof

- e2e: `tests/e2e/examples/038_keyword_coverage_registry/` — the context and
  registry (T038_01–03), coverage (T038_04–07), keywords-hold (T038_08–12), the
  research gates incl. every wrapped `gh` form and the searches no spelling
  list named (T038_13–20), scanner deletion and the obligation surviving an
  unseen delete (T038_21–24), shell fetches of GitHub incl. a URL built from a
  variable (T038_25), the search refusal naming a near-miss scanner file
  (T038_26), and the registry's integrity (T038_27–31): scanners sharing a
  folder name, a cited delete retiring the obligation, an unreadable registry
  refusing, a refused narrowing never shrinking it, and one keyword parser for
  every rule. Scripts run directly (T038_32–33): the judge's prepare on a
  predicate that cannot run, and the eval scorer claiming only the coverage it
  checked.
- eval: `eval/security-scan/` — a real Haiku run with its full toolset
  (WebSearch and WebFetch included) and a skill teaching the convention, scored
  on trajectory health, with deterministic failures for a declared scanner
  deleted before the run ended and for a SCAN-NOTES.md missing from the project.
  The judge is told a scanner WAS covered only when the scorer checked that
  itself (one gh command carrying every keyword); a coverage gate that never
  refused is not that fact — it is also silent when it never ran.
- eval: `eval/security-scan-unprimed/` — the same task with NO skill: the
  refusals' remedies are the only teacher. Its seed's CLAUDE.md says GitHub is
  researched with gh (nothing about scanners), so agents reach for `gh search`
  first and meet `search-needs-declared-scanner` — primed, or reaching for
  WebSearch first, they never did. It is also what showed the search refusal
  must name a misplaced scanner file: an agent that wrote
  `.sloprail/scanners/<name>.yaml` was refused with generic text three times
  and gave up on searching.
