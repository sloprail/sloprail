package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// GitHub research happens against a declared scanner, through gh:
//
//   - github-research-through-gh (PreToolUse) refuses WebSearch outright and a
//     WebFetch of a GitHub content host, pointing at gh + a declared scanner;
//   - search-needs-declared-scanner (PreCommandInvoke) refuses a gh search —
//     however the command line wraps it — until scanner-declared has logged a
//     scanner this session.
//
// Found on real security-scan runs: with WebSearch available Haiku researched
// through it and never declared a scanner, so verify-scanner-coverage (Stop,
// scanner-declared active only) never engaged.

const (
	webRefusal    = "To search GitHub: declare the scanner first"
	searchRefusal = "No scanner is declared in this session"
)

// researchProject installs the example with a stub `gh` under .stub/, so a gh
// call the gates PERMIT runs the stub rather than reaching GitHub. The stub is
// put on PATH by an `export …;` ahead of the command — a sequence, which is also
// one of the wrapped forms the gates must see through.
func researchProject(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.WriteExecutable(proj, ".stub/gh", "#!/bin/sh\necho stub-gh \"$@\"\n")
	e.WriteExecutable(proj, ".stub/curl", "#!/bin/sh\necho stub-curl \"$@\"\n")
	e.WriteExecutable(proj, ".stub/wget", "#!/bin/sh\necho stub-wget \"$@\"\n")
	e.WriteFile(proj, "sub/.keep", "")
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")
	return e, proj
}

// stubbed runs a gh command line against the stub.
func stubbed(command string) string { return `export PATH="$PWD/.stub:$PATH"; ` + command }

// T038_13: WebSearch is refused outright, with the remedy and the gate's name.
func TestT038_13_WebSearchRefused(t *testing.T) {
	e, proj := researchProject(t)
	res := e.Run(proj, "s-038-13", "research auth token leaks", Turns("done",
		harness.ToolUse("t1", "WebSearch", map[string]string{"query": "auth token leak logs github"}),
	))
	if !res.Refused() {
		t.Fatalf("WebSearch was not refused:\n%s", res.Output)
	}
	for _, want := range []string{"WebSearch is not used in this project", webRefusal, "gh search", "github-research-through-gh"} {
		if !res.Saw(want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, res.Output)
		}
	}
}

// T038_14: a WebFetch of a GitHub content host is refused — every host that
// serves repositories, issues, code or gists, however the URL is spelled.
func TestT038_14_WebFetchOfGitHubRefused(t *testing.T) {
	for _, url := range []string{
		"https://github.com/owner/repo/issues/1",
		"https://www.github.com/owner/repo",
		"https://api.github.com/search/issues?q=token+leak",
		"https://gist.github.com/someone/abc123",
		"https://codeload.github.com/owner/repo/zip/main",
		"https://raw.githubusercontent.com/owner/repo/main/README.md",
		"https://gist.githubusercontent.com/someone/abc/raw/x",
		"HTTPS://GitHub.COM/owner/repo",
		"http://github.com:443/owner/repo",
		"https://user@github.com/owner/repo",
		"github.com/owner/repo",
		"https://github.com",
		// The fully-qualified spelling, and the two GitHub content hosts the
		// list missed.
		"https://github.com./owner/repo",
		"https://api.github.com.:443/search/issues?q=token",
		"https://raw.github.com/owner/repo/main/README.md",
		"https://uploads.github.com/repos/owner/repo/releases/1/assets",
	} {
		t.Run(url, func(t *testing.T) {
			e, proj := researchProject(t)
			res := e.Run(proj, "s-038-14", "research auth token leaks", Turns("done",
				harness.ToolUse("t1", "WebFetch", map[string]string{"url": url, "prompt": "summarize"}),
			))
			if !res.Refused() {
				t.Fatalf("WebFetch %s was not refused:\n%s", url, res.Output)
			}
			if !res.Saw(webRefusal) || !res.Saw("github-research-through-gh") {
				t.Errorf("the refusal does not carry the remedy and gate name:\n%s", res.Output)
			}
		})
	}
}

// T038_15: a WebFetch of anything else is allowed — including GitHub-owned
// documentation hosts and URLs that merely MENTION github.com.
func TestT038_15_WebFetchElsewhereAllowed(t *testing.T) {
	for _, url := range []string{
		"https://owasp.org/www-community/vulnerabilities/Insertion_of_Sensitive_Information_into_Log_File",
		"https://docs.github.com/en/rest/search",
		"https://someone.github.io/blog/post",
		"https://github.blog/2024-01-01-a-post/",
		"https://example.com/?u=https://github.com/owner/repo",
		"https://github.com.evil.example/owner/repo",
		"https://notgithub.com/owner/repo",
	} {
		t.Run(url, func(t *testing.T) {
			e, proj := researchProject(t)
			res := e.Run(proj, "s-038-15", "read about token leaks", Turns("done",
				harness.ToolUse("t1", "WebFetch", map[string]string{"url": url, "prompt": "summarize"}),
			))
			if res.Refused() {
				t.Fatalf("WebFetch %s was refused:\n%s", url, res.Output)
			}
		})
	}
}

// T038_16: a gh search with no scanner declared is refused, and the parsing
// sees through every way a command line wraps it — never a regex on the raw
// string.
func TestT038_16_SearchWithoutScannerRefused(t *testing.T) {
	for _, command := range []string{
		`gh search issues "auth token" logs`,
		`cd sub && gh search code "token leak"`,
		`GH_PAGER= gh search repos token-leak`,
		`env GH_PAGER=cat gh search issues token`,
		`command gh search issues token`,
		`bash -c "gh search issues 'auth token leak'"`,
		`true; gh search prs token --limit=5`,
		`echo token | xargs gh search issues`,
		`/usr/local/bin/gh search issues token`,
		`gh search issues token | head -5`,
		`gh api search/issues -f q=token`,
		`gh api "/search/code?q=token+leak"`,
		`gh api https://api.github.com/search/issues -f q=token`,
		`gh api -X GET search/repositories -f q=token`,
		`gh api graphql -f query='{ search(query: "token leak", type: ISSUE, first: 5) { issueCount } }'`,
		// Searches no list of search spellings named — the gate allows a known
		// set of reads instead, and everything else counts as a search.
		`gh issue list -R cli/cli --search "token leak"`,
		`gh pr list -R cli/cli --search=token`,
		`gh issue list -R cli/cli -S token`,
		`gh api graphql -f query='{ search (query: "token leak", type: ISSUE, first: 5) { issueCount } }'`,
		`gh api graphql -F query=@q.graphql`,
		`gh s token`,
		`X=search; gh $X issues token`,
		`gh api $ENDPOINT -f q=token`,
	} {
		t.Run(command, func(t *testing.T) {
			e, proj := researchProject(t)
			res := e.Run(proj, "s-038-16", "research auth token leaks", Turns("done",
				Bash("b1", stubbed(command)),
			))
			if !res.Refused() {
				t.Fatalf("an undeclared gh search was not refused: %s\n%s", command, res.Output)
			}
			if !res.Saw(searchRefusal) || !res.Saw("scanners/token-leaks/scanner.yaml") || !res.Saw("search-needs-declared-scanner") {
				t.Errorf("the refusal does not carry the remedy and gate name:\n%s", res.Output)
			}
			if res.Saw("stub-gh search") {
				t.Errorf("the refused search ran anyway:\n%s", res.Output)
			}
		})
	}
}

// T038_17: with a scanner declared EARLIER IN THE SAME TURN, the search runs —
// the context logs the scanner at the PreFileWrite, before the search's own
// pre-tool check reads the registry. Wrapped forms included.
func TestT038_17_SearchWithDeclaredScannerAllowed(t *testing.T) {
	e, proj := researchProject(t)
	res := e.Run(proj, "s-038-17", "research guardrails", Turns("done",
		Write("w1", "scanners/mine/scanner.yaml", activeScanner),
		Bash("b1", stubbed(`cd sub && gh search repos "guardrail llm agent"`)),
		Bash("b2", stubbed(`gh api search/issues -f q=guardrail`)),
	))
	if res.Refused() {
		t.Fatalf("a search after a declared scanner was refused:\n%s", res.Output)
	}
	if !res.Saw("stub-gh search repos") || !res.Saw("stub-gh api search/issues") {
		t.Errorf("the permitted searches did not run:\n%s", res.Output)
	}
	if blocks := e.BlockingErrorsFrom(proj, "s-038-17", "Stop"); len(blocks) != 0 {
		t.Errorf("coverage was met in one call, yet Stop was refused:\n%v", blocks)
	}
}

// T038_18: a scanner declared in an EARLIER TURN still counts. The context closes
// at the first turn's passing Stop, so a rule reading context[...].active would
// refuse this; the registry persists for the session.
func TestT038_18_ScannerFromEarlierTurnStillCounts(t *testing.T) {
	e, proj := researchProject(t)
	const sess = "s-038-18"
	res := e.Run(proj, sess, "research guardrails", Turns("done",
		Write("w1", "scanners/mine/scanner.yaml", activeScanner),
		Bash("b1", stubbed(`gh search repos guardrail llm agent`)),
	))
	if res.Refused() {
		t.Fatalf("turn 1 refused:\n%s", res.Output)
	}
	if active, _ := e.ContextState(proj, sess, "scanner-declared"); active {
		t.Fatalf("precondition: the context should have closed at the passing Stop")
	}
	res = e.Run(proj, sess, "search a bit more", Turns("done",
		Bash("b2", stubbed(`gh search issues guardrail llm agent`)),
	))
	if res.Refused() {
		t.Fatalf("a search in a later turn, with the scanner declared earlier, was refused:\n%s", res.Output)
	}
}

// T038_19: the rule refuses searching, nothing around it — the scanner.yaml
// write itself and gh calls that read what was already found run with no scanner
// declared.
func TestT038_19_ScannerWriteAndNonSearchGhAllowed(t *testing.T) {
	for _, command := range []string{
		`gh issue view 123 -R cli/cli`,
		`gh repo view cli/cli`,
		`gh pr view 7 -R cli/cli --comments`,
		`gh api repos/cli/cli/issues/123`,
		`gh --version`,
		`gh issue list -R cli/cli --label bug --limit 5`,
		`gh auth status`,
		`gh api -H 'Accept: application/vnd.github.raw' repos/elastic/elasticsearch/contents/README.md`,
	} {
		t.Run(command, func(t *testing.T) {
			e, proj := researchProject(t)
			res := e.Run(proj, "s-038-19", "look at one issue", Turns("done",
				Bash("b1", stubbed(command)),
			))
			if res.Refused() {
				t.Fatalf("a non-search gh call was refused: %s\n%s", command, res.Output)
			}
		})
	}

	e, proj := researchProject(t)
	res := e.Run(proj, "s-038-19w", "declare a scanner", Turns("done",
		Write("w1", "scanners/mine/scanner.yaml", activeScanner),
	))
	if res.Refused() {
		t.Fatalf("writing the scanner itself was refused:\n%s", res.Output)
	}
}

// T038_20: the remedy's own claim — a scanner that already exists (committed
// before this session) is registered by writing it again unchanged, and the
// search then runs.
func TestT038_20_RewritingAnExistingScannerRegistersIt(t *testing.T) {
	e, proj := researchProject(t)
	e.WriteFile(proj, "scanners/mine/scanner.yaml", activeScanner)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "scanner")

	res := e.Run(proj, "s-038-20", "research guardrails", Turns("done",
		Bash("b1", stubbed(`gh search repos guardrail llm agent`)),
	))
	if !res.Refused() || !strings.Contains(res.Output, searchRefusal) {
		t.Fatalf("precondition: a committed-but-unregistered scanner should not count yet:\n%s", res.Output)
	}

	res = e.Run(proj, "s-038-20b", "research guardrails", Turns("done",
		Write("w1", "scanners/mine/scanner.yaml", activeScanner),
		Bash("b1", stubbed(`gh search repos guardrail llm agent`)),
	))
	if res.Refused() {
		t.Fatalf("re-writing the existing scanner did not register it:\n%s", res.Output)
	}
}

// T038_25: fetching GitHub content from the SHELL is refused like a WebFetch —
// found on a real run that, refused WebSearch, read issues with `curl
// https://api.github.com/repos/…` and files with `curl https://raw.githubusercontent.com/…`.
// Other URLs, and URLs that only mention GitHub, still fetch.
func TestT038_25_ShellFetchOfGitHubRefused(t *testing.T) {
	for _, command := range []string{
		`curl -s https://api.github.com/repos/owner/repo/issues/245 | jq .title`,
		`curl -sL "https://raw.githubusercontent.com/owner/repo/main/SECURITY.md"`,
		`curl "https://api.github.com/search/issues?q=token+leak"`,
		`curl --url=https://github.com/owner/repo`,
		`wget -qO- https://github.com/owner/repo/issues/1`,
		`cd sub && curl -H 'Accept: application/json' https://api.github.com/repos/owner/repo`,
		`bash -c 'curl https://gist.github.com/someone/abc'`,
		`curl -s https://github.com./owner/repo`,
		`curl -sL https://raw.github.com/owner/repo/main/SECURITY.md`,
		// A URL built from a variable: the parser cannot resolve $U, and the
		// argument reads `/search/issues`.
		`U=https://api.github.com; curl -s $U/search/issues?q=token`,
		`H=api.github.com; curl -s "https://$H/repos/owner/repo"`,
		`curl -s "$(printf https://api.github.com)/search/issues"`,
	} {
		t.Run(command, func(t *testing.T) {
			e, proj := researchProject(t)
			res := e.Run(proj, "s-038-25", "read an issue", Turns("done",
				Bash("b1", stubbed(command)),
			))
			if !res.Refused() {
				t.Fatalf("a shell fetch of GitHub was not refused: %s\n%s", command, res.Output)
			}
			if !res.Saw("bypasses gh") || !res.Saw("gh issue view") || !res.Saw("github-research-through-gh") {
				t.Errorf("the refusal does not carry the remedy and gate name:\n%s", res.Output)
			}
		})
	}

	for _, command := range []string{
		`curl -s https://owasp.org/www-project-top-ten/`,
		`curl -s "https://example.com/?u=https://github.com/owner/repo"`,
		`wget -qO- https://docs.github.com/en/rest`,
		`echo https://github.com/owner/repo`,
		`curl -s https://github.com.evil.example/owner/repo`,
		`Q=token; curl -s "https://owasp.org/?q=$Q"`,
	} {
		t.Run(command, func(t *testing.T) {
			e, proj := researchProject(t)
			res := e.Run(proj, "s-038-25b", "read a page", Turns("done",
				Bash("b1", stubbed(command)),
			))
			if res.Refused() {
				t.Fatalf("a non-GitHub fetch was refused: %s\n%s", command, res.Output)
			}
		})
	}
}

// T038_26: the search refusal names a near-miss scanner and says where a
// scanner must be. Found on a real unprimed run: the agent wrote
// `.sloprail/scanners/auth-token-logs.yaml`, was refused with the generic text
// three times, and gave up on searching. And a scanner in the right place that
// this session never registered is named with the fix for it.
func TestT038_26_SearchRefusalNamesTheNearMiss(t *testing.T) {
	t.Run("wrong path", func(t *testing.T) {
		e, proj := researchProject(t)
		res := e.Run(proj, "s-038-26a", "research auth token leaks", Turns("done",
			Write("w1", ".sloprail/scanners/auth-token-logs.yaml", activeScanner),
			Bash("b1", stubbed(`gh search issues "auth token leaked logs"`)),
		))
		if !res.Refused() {
			t.Fatalf("a search after a misplaced scanner was not refused:\n%s", res.Output)
		}
		if !res.Saw("Not a scanner: .sloprail/scanners/auth-token-logs.yaml") || !res.Saw("named exactly scanner.yaml") {
			t.Errorf("the refusal does not name the misplaced file and the right shape:\n%s", res.Output)
		}
	})

	t.Run("right path, not registered", func(t *testing.T) {
		e, proj := researchProject(t)
		e.WriteFile(proj, "scanners/mine/scanner.yaml", activeScanner)
		e.Git(proj, "add", "-A")
		e.Git(proj, "commit", "-m", "scanner")
		res := e.Run(proj, "s-038-26b", "research guardrails", Turns("done",
			Bash("b1", stubbed(`gh search repos guardrail llm agent`)),
		))
		if !res.Refused() || !res.Saw("scanners/mine/scanner.yaml is in the right place but was not registered") {
			t.Fatalf("the refusal does not explain the unregistered scanner:\n%s", res.Output)
		}
	})
}
