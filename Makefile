# sloprail — build and local installation.
#
# The shape is a10n's (see ~/ws/horizon-37/a10n-cli/Makefile), and two things
# are copied from it deliberately:
#
#   codesign --sign -   on macOS a binary copied OVER an existing signed one is
#                       killed by the kernel at exec with a signature error that
#                       reads like anything but a signing problem. Every copy is
#                       re-signed, unconditionally.
#
#   install BESIDE the  the services are found as siblings of the running
#   root binary         binary (internal/subbin, step 2). Deriving the
#                       destination from where `sr` already is means the set
#                       lands wherever the user's PATH already points, rather
#                       than in a directory this file guessed.
#
# What is NOT copied is a10n's assumption that `which a10n` always answers.
# sloprail has no installed copy on a fresh machine, and a10n's target silently
# copies to `/` when the shell substitution comes back empty. See INSTALL_DIR.

BIN_DIR  := bin
SERVICES := sr sr-session sr-file sr-mark sr-agent sr-eval sr-checks
BINARIES := $(addprefix $(BIN_DIR)/,$(SERVICES))

# VERSION is what `sr-session --version` (etc.) reports, and what the plugin's
# installation check compares against plugin.json's own version to decide
# whether an installed binary needs upgrading — see internal/version.Version's
# doc comment. Bare semver, no leading v, matching plugin.json's own field.
#
# Default: derive it from the nearest reachable tag (git describe), stripping
# the "v" release.yml's tags carry — this makes a local `make build` off a
# tagged commit report the real version with no extra step. `make release`
# always runs in CI right after a tag push, where this resolves to exactly
# that tag; a caller building a specific version (or with no tags reachable,
# e.g. a shallow clone) can still override: `make build VERSION=0.2.1`.
VERSION := $(patsubst v%,%,$(shell git describe --tags --match 'v*' --abbrev=0 2>/dev/null))
ifeq ($(VERSION),)
VERSION := dev
endif
LDFLAGS := -X github.com/sloprail/sloprail/internal/version.Version=$(VERSION)

# Where distribute-local installs.
#
#   1. PREFIX, if the caller set it   — make distribute-local PREFIX=~/bin
#   2. the directory holding the `sr` already on $PATH  (the upgrade case)
#   3. $GOBIN, else $GOPATH/bin       (the first-install default)
#
# (3) is the first-install answer, and it is a default rather than an error
# because it is the directory `go install ./services/...` would have used — the
# arrangement internal/subbin documents as ordinary. `make where` prints the
# resolved value; distribute-local echoes it before copying anything.
GOPATH_BIN := $(shell go env GOBIN)
ifeq ($(GOPATH_BIN),)
GOPATH_BIN := $(shell go env GOPATH)/bin
endif
INSTALLED_SR := $(shell command -v sr 2>/dev/null)

ifneq ($(PREFIX),)
INSTALL_DIR := $(PREFIX)
else ifneq ($(INSTALLED_SR),)
INSTALL_DIR := $(patsubst %/,%,$(dir $(INSTALLED_SR)))
else
INSTALL_DIR := $(GOPATH_BIN)
endif

.PHONY: build distribute-local where test test-unit test-services test-e2e clean tidy check

build:
	@mkdir -p $(BIN_DIR)
	@for s in $(SERVICES); do \
		echo "go build -ldflags \"$(LDFLAGS)\" -o $(BIN_DIR)/$$s ./services/$$s"; \
		go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$$s ./services/$$s || exit 1; \
	done

# where prints the install destination and why, without touching anything. Run
# it before distribute-local on a machine you care about.
where:
	@echo "install dir: $(INSTALL_DIR)"
	@if [ -n "$(PREFIX)" ]; then echo "  because: PREFIX was set"; \
	elif [ -n "$(INSTALLED_SR)" ]; then echo "  because: sr is already installed at $(INSTALLED_SR)"; \
	else echo "  because: no sr on \$$PATH — defaulting to the Go bin dir"; fi

# distribute-local installs the whole set into ONE directory, which is what
# sibling resolution requires: `sr session start` execs the sr-session next to
# it, so a set split across two directories resolves through $PATH by luck.
#
# The whole set is copied every time, including on an upgrade. Copying only the
# changed ones is how a stale sr-session outlives the sr that dispatches to it.
distribute-local: build
	@if [ -z "$(INSTALL_DIR)" ]; then \
		echo "make: cannot determine an install directory."; \
		echo "  No sr on \$$PATH and no GOBIN/GOPATH from \`go env\`."; \
		echo "  Run: make distribute-local PREFIX=/somewhere/on/your/PATH"; \
		exit 1; \
	fi
	@if [ ! -d "$(INSTALL_DIR)" ]; then \
		echo "creating $(INSTALL_DIR)"; \
		mkdir -p "$(INSTALL_DIR)" || exit 1; \
	fi
	@echo "installing $(words $(SERVICES)) binaries into $(INSTALL_DIR)"
	@for s in $(SERVICES); do \
		cp $(BIN_DIR)/$$s "$(INSTALL_DIR)/$$s" || exit 1; \
		codesign --sign - --force "$(INSTALL_DIR)/$$s" 2>/dev/null || true; \
		echo "  $(INSTALL_DIR)/$$s"; \
	done
	@case ":$$PATH:" in \
		*":$(INSTALL_DIR):"*) ;; \
		*) echo; echo "NOTE: $(INSTALL_DIR) is not on your \$$PATH — add it, or the hooks will not find sr-session.";; \
	esac

# release cross-compiles the whole service set for every (GOOS,GOARCH) a
# stranger's machine is likely to be, and packages each platform's set into one
# tar.gz — install.sh's whole job is picking the right one and unpacking it.
#
# One archive PER PLATFORM, not per binary: sibling resolution
# (internal/subbin) requires the set to land in one directory together, so
# shipping them separately would make install.sh reassemble what this target
# could just ship pre-assembled.
#
# No CGO: sqlite (modernc.org/sqlite) is already pure Go, so CGO_ENABLED=0 is
# free and is what makes a linux/arm64 binary buildable from a darwin/amd64 CI
# runner with no cross toolchain installed.
RELEASE_DIR := dist
RELEASE_PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64

# bump-version updates every plugin.json + marketplace.json to VERSION,
# lockstep with the repo's own release. The ordinary way to cut a release is
# `make cut-release VERSION=0.2.1` then, once that PR is merged, `make
# cut-release-tag VERSION=0.2.1` (both scripts/cut-release.sh) — between them
# they run this, commit, push, open the PR, and tag, so nobody has to do the
# sequence by hand. This target (and scripts/bump-version.sh directly) stays
# for a local dry run of what that script would write. VERSION is bare semver
# (0.2.0), no leading v.
.PHONY: bump-version
bump-version:
	@if [ -z "$(VERSION)" ]; then \
		echo "make: VERSION is required — make bump-version VERSION=0.2.0" >&2; \
		exit 1; \
	fi
	@./scripts/bump-version.sh "$(VERSION)"

# cut-release runs scripts/cut-release.sh open: bumps, commits, pushes a
# release/vX.Y.Z branch and opens the PR that starts a release — see that
# script's own header for why this has to run as a real person (you) rather
# than as a GitHub Action.
.PHONY: cut-release
cut-release:
	@if [ -z "$(VERSION)" ]; then \
		echo "make: VERSION is required — make cut-release VERSION=0.2.1" >&2; \
		exit 1; \
	fi
	@./scripts/cut-release.sh open "$(VERSION)"

# cut-release-tag runs scripts/cut-release.sh tag: once cut-release's PR has
# been merged, this tags main — which is what actually triggers release.yml
# to build and publish. Also has to run as a real person, for the same
# GITHUB_TOKEN-anti-recursion reason the bump PR does; see the script's own
# header.
.PHONY: cut-release-tag
cut-release-tag:
	@if [ -z "$(VERSION)" ]; then \
		echo "make: VERSION is required — make cut-release-tag VERSION=0.2.1" >&2; \
		exit 1; \
	fi
	@./scripts/cut-release.sh tag "$(VERSION)"

# verify-version fails if the pushed tag and the committed plugin.json/
# marketplace.json versions disagree — the release.yml gate that catches a
# tag pushed by hand, outside scripts/cut-release.sh, before its bump landed.
# TAG is the full tag (v0.2.0); the leading v is stripped to compare against
# plugin.json's bare-semver field.
#
# marketplace.json is checked too, not just marketplace/plugins/*/plugin.json:
# it carries its OWN copy of each plugin's version (bump-version.sh writes
# both), and a tag whose plugin.json files matched but whose marketplace.json
# was left stale would ship a marketplace listing lying about what it points
# to — the exact gap this loop closes.
.PHONY: verify-version
verify-version:
	@if [ -z "$(TAG)" ]; then \
		echo "make: TAG is required — make verify-version TAG=v0.2.0" >&2; \
		exit 1; \
	fi
	@want="$${TAG#v}"; \
	for f in $$(find marketplace/plugins -maxdepth 3 -name plugin.json -path '*/.claude-plugin/*'); do \
		got="$$(jq -r .version "$$f")"; \
		if [ "$$got" != "$$want" ]; then \
			echo "make: $$f has version $$got, tag $(TAG) wants $$want — run make cut-release VERSION='$$want' instead of tagging by hand" >&2; \
			exit 1; \
		fi; \
	done
	@want="$${TAG#v}"; \
	for got in $$(jq -r '.plugins[].version' .claude-plugin/marketplace.json); do \
		if [ "$$got" != "$$want" ]; then \
			echo "make: .claude-plugin/marketplace.json has a plugin at version $$got, tag $(TAG) wants $$want — run make cut-release VERSION='$$want' instead of tagging by hand" >&2; \
			exit 1; \
		fi; \
	done
	@echo "verify-version: all plugin.json and marketplace.json match $(TAG)"

.PHONY: release
release:
	@rm -rf $(RELEASE_DIR)
	@mkdir -p $(RELEASE_DIR)
	@for plat in $(RELEASE_PLATFORMS); do \
		os=$${plat%/*}; arch=$${plat#*/}; \
		outdir=$(RELEASE_DIR)/sloprail-$$os-$$arch; \
		mkdir -p "$$outdir"; \
		echo "building $$os/$$arch"; \
		for s in $(SERVICES); do \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o "$$outdir/$$s" ./services/$$s || exit 1; \
		done; \
		tar -C $(RELEASE_DIR) -czf $(RELEASE_DIR)/sloprail-$$os-$$arch.tar.gz sloprail-$$os-$$arch; \
		rm -rf "$$outdir"; \
	done
	@( cd $(RELEASE_DIR) && shasum -a 256 *.tar.gz > checksums.txt )
	@echo "release archives in $(RELEASE_DIR)/"

# check is what has to be clean before anything is committed.
check:
	go build ./...
	go vet ./...
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt:"; echo "$$out"; exit 1; fi

# The suite runs with -p 1: one test binary at a time.
#
# NOT a style preference. The e2e tests each build the services into their
# own temp tree and run a real agent against them; a parallel `-race ./...`
# across 46 e2e packages ran the disk out of space. -p 1 is the constraint, and
# the split into unit/services/e2e is so a failure in the fast half is reported
# in seconds rather than after the slow half finishes.
test: test-unit test-services test-e2e

# tests/repo checks the repository's own files (every shipped shell script
# parses under bash), so it rides with the unit tests: no binary, no mock.
test-unit:
	go test -p 1 -count=1 ./internal/... ./tests/repo/...

test-services:
	go test -p 1 -count=1 ./services/...

# The e2e suites drive a10n-claude-mock (github.com/sloprail/harness-mocks), a
# stand-in `claude`, as the agent. `make mock` installs the version pinned in
# tests/e2e/harness/MOCK_VERSION into .bin/, where the harness looks first — so
# CI and every contributor run the same mock, and a mock bump is a reviewed
# change to that file. Built from source by `go install`, so it works on any OS.
# The .bin/a10n-claude-mock.<version> stamp makes a repeat run a no-op.
MOCK_VERSION := $(shell cat tests/e2e/harness/MOCK_VERSION)
MOCK_STAMP := .bin/a10n-claude-mock.$(MOCK_VERSION)

.PHONY: mock
mock: $(MOCK_STAMP)

$(MOCK_STAMP): tests/e2e/harness/MOCK_VERSION
	@mkdir -p .bin
	GOBIN=$(CURDIR)/.bin go install github.com/sloprail/harness-mocks/claude-mock@$(MOCK_VERSION)
	mv .bin/claude-mock .bin/a10n-claude-mock
	rm -f .bin/a10n-claude-mock.v*
	touch $@

# -timeout 30m: each e2e package builds binaries and drives a mock agent.
test-e2e: mock
	go test -p 1 -count=1 -timeout 30m ./tests/...

# Sharded e2e for CI. The whole suite run with -p 1 (the disk constraint above)
# grew past the CI runner's per-job wall-clock as the corpus of use-case e2e
# expanded, so CI runs it as a matrix: several jobs, each -p 1 (disk stays low),
# each a disjoint slice of ./tests/... . SHARD names the slice; the union of the
# slices below is exactly `go list ./tests/...`, so nothing is dropped. Keep this
# examples/examples2/examples3 split ./tests/e2e/examples/... by
# scripts/examples-shard.sh, which DISCOVERS the packages with `go list` (so a
# new example is never dropped) and balances them by greedy bin packing.
# Keep this list and the workflow matrix in lockstep — a package matching no slice would
# silently never run in CI.
#
#   make test-e2e-shard SHARD=session
test-e2e-shard: mock
	@case "$(SHARD)" in \
	  session)  go test -p 1 -count=1 -timeout 30m $$(go list ./tests/e2e/session/... | grep -vE '/session/(025_subdirectory_hooks|028_trajectory_describe|029_trajectory_cite|031_trajectory_normalize)$$') ;; \
	  session2) go test -p 1 -count=1 -timeout 30m \
	              ./tests/e2e/session/025_subdirectory_hooks/... \
	              ./tests/e2e/session/028_trajectory_describe/... \
	              ./tests/e2e/session/029_trajectory_cite/... \
	              ./tests/e2e/session/031_trajectory_normalize/... ;; \
	  pre_tool) go test -p 1 -count=1 -timeout 30m ./tests/e2e/pre_tool/... ;; \
	  examples)  go test -p 1 -count=1 -timeout 30m $$(scripts/examples-shard.sh 1 3) ;; \
	  examples2) go test -p 1 -count=1 -timeout 30m $$(scripts/examples-shard.sh 2 3) ;; \
	  examples3) go test -p 1 -count=1 -timeout 30m $$(scripts/examples-shard.sh 3 3) ;; \
	  rest)     go test -p 1 -count=1 -timeout 30m \
	              ./tests/e2e/subagent/... \
	              ./tests/e2e/gate/... \
	              ./tests/e2e/context/... \
	              ./tests/e2e/fileguard/... \
	              ./tests/e2e/changeset/... \
	              ./tests/e2e/checks/... \
	              ./tests/e2e/grounding/... \
	              ./tests/e2e/structure/... \
	              ./tests/e2e/proxy/... \
	              ./tests/e2e/engine_repo_judges/... \
	              ./tests/e2e/declarations/... \
	              ./tests/e2e/authoring/... \
	              ./tests/e2e/harness/... ;; \
	  plugins)  $(MAKE) test-plugins-e2e ;; \
	  *) echo "test-e2e-shard: unknown SHARD='$(SHARD)' (want: session|session2|pre_tool|examples|examples2|examples3|rest|plugins)" >&2; exit 2 ;; \
	esac

# Plugin-local e2e modules. Each marketplace plugin that ships its own tests/ Go
# module (its own go.mod, importing the shared harness via a replace) is a
# SELF-CONTAINED suite — the "each plugin self-tests" model. They are NOT part of
# `go list ./tests/...` (a separate module), so they are run explicitly here.
#
# DISCOVERED, not hand-listed: every marketplace/plugins/*/tests directory
# that has its own go.mod is picked up automatically, one `go test` per
# module dir. A new plugin growing a tests/ module needs no edit here — the
# alternative (a hand-maintained list) is exactly what silently drops a
# plugin's suite from CI the day someone forgets to add a line to it.
.PHONY: test-plugins-e2e
test-plugins-e2e: mock
	@set -e; \
	dirs="$$(find marketplace/plugins -mindepth 2 -maxdepth 2 -type d -name tests -exec test -f '{}/go.mod' \; -print | sort)"; \
	if [ -z "$$dirs" ]; then \
	  echo "test-plugins-e2e: no marketplace/plugins/*/tests module found" >&2; \
	  exit 1; \
	fi; \
	for d in $$dirs; do \
	  echo "== plugin e2e: $$d =="; \
	  ( cd "$$d" && go test -p 1 -count=1 -timeout 30m ./... ); \
	done

tidy:
	go mod tidy

clean:
	rm -rf $(BIN_DIR)
