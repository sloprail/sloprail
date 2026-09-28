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
| `github-research-through-gh` | gate | `PreToolUse`: `WebSearch`, `WebFetch`. `PreCommandInvoke`: `curl`/`wget`/httpie | any WebSearch; a fetch of a GitHub content host, of URLs from a file, or of a URL the line hides |
| `search-needs-declared-scanner` | gate | `PreCommandInvoke`: any line running or naming `gh` | a gh call that searches GitHub (or that it cannot see into), with no scanner declared this session |
| `verify-scanner-coverage` | gate | `Stop`, while `scanner-declared` is active | a declared scanner no single gh call covered |
| `scanner-keywords-hold` | file-guard, preventive, `deletions: include` | the scanner file | dropping a keyword, or deleting the scanner, without the user's words; records what the user did ask for |

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
  by a shell command, whose bytes a Pre event cannot know. Here too the entry
  becomes the **union** of what is owed and the file's keywords. It used to
  become exactly the file's keywords, trusting that anything which dropped one
  had got past `scanner-keywords-hold` — but a write the engine cannot parse
  (`python3 -c "open(…).write(…)"`) narrowed a scanner declared this session,
  reached Stop as a `PostFileCreate`, and a create "drops nothing", so the
  narrowing landed unasked and a search for the one keyword left passed.

The registry **never shrinks** on its own. A scanner declared this session stays
owed its search, in full, even if its file is later narrowed or switched off by
a write no rule could read, or deleted by a route no rule saw. The only ways an
obligation goes are what the **user** asked for, cited in their own words and
judged to ask for it: a delete retires the scanner, a drop narrows it (see
[what the user asked for](#retiring-or-narrowing-what-the-user-asked-for)).

Keywords are read the way YAML writes a list: a block list (items at any
indentation, quoted or plain, a plain item continued on more-indented lines,
`>`/`|` block scalars, `'it''s'`), a flow list (`keywords: [a, "b c"]`, over
several lines too), `keywords :` with a space, and `active: true`/`yes`/`on` in
any case. Anything else declares no keyword, and the search refusal says so by
name — it used to tell the agent to "write the file again unchanged", which for
a file that can never register was a loop.

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
  fetchable, as does every other URL. The **host** is what is compared, parsed
  the way a browser parses the URL: tabs and newlines dropped, any run of `/` or
  `\` after `http(s):` taken as the start of the host (`https:github.com/…`,
  `https:\\github.com\…`), userinfo and port dropped, percent-decoded
  (`git%68ub.com`), trailing dots dropped (`github.com.`) — a regex over the raw
  URL was evaded by each of those. `https://example.com/?u=github.com` and
  `github.com.evil.example` are not GitHub. **The same hosts fetched from the
  shell** — `curl`, `wget`, httpie/`xh` — are refused too: once WebSearch was
  refused, a real run read issues with `curl https://api.github.com/repos/…` and
  files from `raw.githubusercontent.com`, and a curl of `api.github.com/search`
  is a search no scanner governs. Two things the parsed arguments cannot show
  are refused as well: `curl -K`/`--config` and `wget -i`/`--input-file`, which
  take their URLs from a file; and a GitHub host that can **feed** the fetch
  without being one of its arguments — put into a variable
  (`U=https://api.github.com; curl $U/search/issues` parses as
  `curl /search/issues`) **and expanded in the fetch's pipeline**, a
  `$(…)`/`${…}` there, or an earlier stage of that pipeline (`echo URL | xargs
  curl`). A variable only assigned and used elsewhere
  (`REPO=https://github.com/o/r; echo $REPO; curl https://example.com/`) feeds
  nothing. A mention that cannot feed it is left
  alone: after the fetch in its pipeline (`curl … | grep github.com`) or in
  another command of the line (`curl …; git commit -m "…github.com/…"`). A
  literal address in GitHub's published web/API ranges (140.82.112.0/20,
  143.55.64.0/20, 192.30.252.0/22, 185.199.108.0/22, 2606:50c0::/32,
  2a0a:a440::/29) counts as GitHub too.
  The remedy names the gh equivalents (`gh issue view`, `gh api repos/…/contents/…`).
- **`search-needs-declared-scanner`** (`PreCommandInvoke`, every line that runs
  or names `gh`). Until a scanner is declared, a gh call that **searches GitHub**
  is refused: `gh search …` (not `--help`); `gh api` on a search endpoint or
  graphql, the endpoint resolved first — host and query stripped,
  percent-decoded, `.`/`..` collapsed, since `gh api 'repos/../search/issues?q=…'`
  searched live — with any `..` or no visible endpoint counting as a search;
  `gh issue|pr|label list --search/-S`, short flags read as gh bundles them
  (`-lSecurity` is a label, `-wS` is a search); and what the rule cannot see
  into: an alias or extension (gh does not let one shadow a built-in — but `co`
  is itself only a default alias, and `gh alias set co 'search issues'
  --clobber` redefines it), `gh extension exec <name>`, and a gh the line names
  that the parse does not account for (`eval "gh search …"`,
  `python3 -c "os.system('gh search …')"`). A mention the parse DOES account for
  runs freely — but only when **every program in the command is one known not
  to execute its input**: `echo`, `printf`, `cat`, `tee`, `grep`, `head`/`tail`,
  `jq`, `which`, `type`, `command`, `ls`, `cp`/`mv`/`rm`/`mkdir`, `curl`,
  `sr-file`, `gh` itself, and `git` on its ordinary subcommands (no `-c`, no
  alias). Then an argument (`git commit -m "fix gh auth"`, `which gh`, `grep -c
  gh`, `gh pr create --title "Update gh workflow"`) or a heredoc or here-string
  (`cat > NOTES.md <<EOF`, `git commit -F - <<EOF`) is data. Anything else on
  the line — an interpreter, `xargs`, `make`, `awk` and `sed` (they can run
  commands: `system()`, `e`), `env -S`, a script run by any path that is not a
  system bin directory (`./s.sh`, `/tmp/zzs`, `PATH=.:$PATH zzs`), `git -c
  alias.x='!…'` — may turn text into a command, so every mention not parsed as
  a gh invocation counts as a hidden search. (A list of code-RUNNERS was always
  one short; this is a list of what is known safe.) A gh whose subcommand the
  parser cannot see (`echo search issues x | xargs gh`, `gh $(echo search …)`,
  `A="search …"; gh $A`) is a search too; only the bare command `gh` is not. A `$(gh …)` inside an unquoted heredoc
  is counted once, as the invocation it is. All other gh work — `gh pr create`, `gh repo clone`, `gh pr
  checks`, `gh run list`, `gh issue -R o/r view 1` — runs with or without a
  scanner. Both earlier
  versions were wrong one way: a list of search spellings was measured short
  (`gh issue list --search`, `search (` with a space, a gh alias,
  `X=search; gh $X …` all searched), and an allowlist of reads refused ordinary
  gh calls and called them searches. Invocations are the engine's **parsed**
  ones, so `cd x && gh …`, `FOO=1 gh …`, `env …`, `command gh …`, `bash -c "gh …"`,
  `xargs gh …`, `caffeinate`/`script` wrappers, a full `/usr/local/bin/gh` path
  and quoted arguments are all the same `gh` invocation. The scanner write
  itself is never refused.

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
THESE keywords to go. "Drops" is measured against the file before the change
**and** what the registry holds owed: a scanner emptied behind every rule's
back (a write the engine cannot parse) compared against the file alone dropped
nothing on its later delete — no citation, no judge — and was retired. The judge's `prepare` skips the model only on
`drops-keywords.sh`'s decided "drops nothing" (exit 1): a predicate that could
not run at all — not executable, missing, crashed — used to read as "drops
nothing" too, skip the judge, and let any quote of the user's admit the drop.

`deletions: include`, because deleting the scanner drops every keyword at once.
`rm -rf scanners/<name>` — the directory, as the real run did it — reaches the
guard as a `PreFileDelete` of the scanner file inside: the engine expands a
recursive removal of a directory (`rm -r`/`-R`/`--recursive` or an
abbreviation of it, `git rm -r`, `mv` or `git mv` of it) into one delete per file it
holds, even past its read budget (the files it did not read carry
`oldContentKnown: false`, and the guard, unable to see what the scanner held,
asks for the user's words). A delete the engine cannot see at all
(`find … -delete`, a script, a folder padded past 1000 files — see below)
still does not clear the obligation of a scanner declared this session — the
registry keeps it and the context stays open.

### Retiring or narrowing: what the user asked for

A change the user asked for — its citation of their own words resolved on the
event, and judged to be what they ask — is **recorded** by the guard's last
check, `record-admitted.sh`, which runs only once the checks before it admitted
the event:

- a **delete retires** the scanner: `retired:<folder>`. Without that, a scanner
  declared this session stayed owed forever, and every later Stop was refused,
  telling the agent to search for a scanner the user had removed;
- a **drop narrows** it: `narrowed:<folder>` holds what the scanner still
  declares, and becomes what is owed.

Both are recorded at the declaration's current stamp (`stamp:<folder>`, which
`scanner-declared` renews at every declaration) and count only while it still
matches, so declaring the scanner again makes it owed in full again; a
retirement also needs the file really gone, so a delete some other rule refused
leaves it owed. Neither is recorded without the user's citation on the event —
the check does not infer "asked for" from having been reached, since a change
that drops nothing needs no citation to reach it. A delete no rule saw never
reaches the check and records nothing.

## What it does not catch

- **Paraphrase.** Coverage is literal keywords; a search for a synonym does not
  count, by design — the scanner names its own words.
- **Research through another channel** the gates do not know: an MCP GitHub
  server, a script that fetches for the agent, a fetching program not in the
  list. Add a trigger for it if a project has one.
- **Parsing is a correctness aid, not a security boundary**: a program named by
  a variable (`$GH search …`) or a decoded payload is not visible to
  `event.invocations`. The search gate counts a gh it can SEE named on the line
  but not accounted for by the parse — inside code (`eval`, `python3 -c`, an
  unparsed `sh -c`, a heredoc or here-string fed to `bash`, `python3 -`, or
  piped on to `sh`, text piped into a shell, a script run by its path). A
  heredoc or here-string that feeds no code, on a command that runs none, is
  data and runs (`cat > NOTES.md <<EOF … gh search … EOF`, `git commit -F -
  <<EOF`). The cost: a command that both runs something outside the safe list
  and merely mentions gh (`git commit -m "fix gh auth" && ./gradlew test`,
  `git log | awk '{print $1}'; echo "gh done"`, `awk '$1=="gh"'`) is refused
  until a scanner exists; the refusal says to run the code-running command in a
  separate call. A gh whose name is itself hidden (`G=g; ${G}h search …`,
  `eval "g""h search …"`, base64) is not caught.
- **Fetches this rule cannot see**: a GitHub host named only in an earlier
  command (`export API=https://api.github.com`, then `curl $API/…`), a host
  spliced from pieces (`H=git; curl https://${H}hub.com/…`), a URL a script or
  program builds, a GitHub address outside the listed ranges or spelled another
  way (`https://2354212870/`, hex or octal), a `Host:` header aimed at an
  address this rule does not know, or a fetching program not in the list
  (`python3 -c "urllib…"`, `nc`).
- **A folder padded past 1000 files.** The engine predicts a recursive
  removal's deletes up to 1000 files; past that it predicts none, so padding a
  scanner's folder with files hides its `rm -rf` from the preventive check.
  (Padding it with BYTES no longer does: every file is still predicted, just
  unread.) A scanner declared this session stays owed in the registry, so Stop
  still refuses without a covering search; a committed scanner never declared
  this session is not in the registry, and its loss surfaces only at Stop, as
  a `PostFileDelete` the guard refuses for want of the user's words — after the
  file is already gone.

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
  checked (never from a heredoc's text or a refused call). The second review
  (T038_34–41): an emptied scanner not retired uncited, an unseen narrowing not
  shrinking the registry, a byte-padded folder's delete still refused, a refused
  search not counting as coverage, the near-miss hint that registers when
  followed, `git rm -r`, a declaration whose stamp cannot be recorded not
  entering, and every list shape the parser reads; the last check recording
  only on the user's citation, and a cited drop narrowing what is owed
  (T038_42–43). Every scanner script reads a Post kind's `newContent` only when
  `newContentKnown` is true, and treats an unread settled file as undecidable
  (the registry keeps what is owed; the citation applies).
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
