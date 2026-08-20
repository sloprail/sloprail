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
SERVICES := sr sr-session sr-file sr-mark sr-agent
BINARIES := $(addprefix $(BIN_DIR)/,$(SERVICES))

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
		echo "go build -o $(BIN_DIR)/$$s ./services/$$s"; \
		go build -o $(BIN_DIR)/$$s ./services/$$s || exit 1; \
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

test-unit:
	go test -p 1 -count=1 ./internal/...

test-services:
	go test -p 1 -count=1 ./services/...

# -timeout 30m: each e2e package builds binaries and drives a mock agent.
test-e2e:
	go test -p 1 -count=1 -timeout 30m ./tests/...

# Sharded e2e for CI. The whole suite run with -p 1 (the disk constraint above)
# grew past the CI runner's per-job wall-clock as the corpus of use-case e2e
# expanded, so CI runs it as a matrix: several jobs, each -p 1 (disk stays low),
# each a disjoint slice of ./tests/... . SHARD names the slice; the union of the
# slices below is exactly `go list ./tests/...`, so nothing is dropped. Keep this
# list and the workflow matrix in lockstep — a package matching no slice would
# silently never run in CI.
#
#   make test-e2e-shard SHARD=session
test-e2e-shard:
	@case "$(SHARD)" in \
	  session)  go test -p 1 -count=1 -timeout 30m $$(go list ./tests/e2e/session/... | grep -vE '/session/(025_subdirectory_hooks|028_trajectory_describe|029_trajectory_cite|031_trajectory_normalize)$$') ;; \
	  session2) go test -p 1 -count=1 -timeout 30m \
	              ./tests/e2e/session/025_subdirectory_hooks/... \
	              ./tests/e2e/session/028_trajectory_describe/... \
	              ./tests/e2e/session/029_trajectory_cite/... \
	              ./tests/e2e/session/031_trajectory_normalize/... ;; \
	  pre_tool) go test -p 1 -count=1 -timeout 30m ./tests/e2e/pre_tool/... ;; \
	  examples) go test -p 1 -count=1 -timeout 30m ./tests/e2e/examples/... ;; \
	  rest)     go test -p 1 -count=1 -timeout 30m \
	              ./tests/e2e/revalidation/... \
	              ./tests/e2e/subagent/... \
	              ./tests/e2e/gate/... \
	              ./tests/e2e/context/... \
	              ./tests/e2e/fileguard/... \
	              ./tests/e2e/structure/... \
	              ./tests/e2e/proxy/... \
	              ./tests/e2e/engine_repo_judges/... \
	              ./tests/e2e/declarations/... \
	              ./tests/e2e/authoring/... \
	              ./tests/e2e/harness/... ;; \
	  *) echo "test-e2e-shard: unknown SHARD='$(SHARD)' (want: session|session2|pre_tool|examples|rest)" >&2; exit 2 ;; \
	esac

tidy:
	go mod tidy

clean:
	rm -rf $(BIN_DIR)
