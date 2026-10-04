#!/bin/sh
# sloprail install.sh — puts the sr* binaries on a stranger's machine in one
# command, with nothing beyond what Claude Code already needs: POSIX sh, curl
# (or wget), and tar. No Go toolchain required.
#
#   curl -fsSL https://raw.githubusercontent.com/sloprail/sloprail/main/install.sh | sh
#
# What it does:
#   1. detects the platform (darwin/linux, amd64/arm64)
#   2. downloads that platform's archive from the latest GitHub Release
#      (sloprail/sloprail), verifies it against the release's checksums.txt
#   3. unpacks sr, sr-session, sr-file, sr-mark, sr-agent, sr-eval, sr-checks into one directory —
#      $SLOPRAIL_INSTALL_DIR if set, else ~/.local/bin — because sibling
#      resolution (internal/subbin) requires the set to be installed together
#   3b. also installs a10n-claude-mock (pinned, see below) next to them
#   4. warns, once, if that directory is not on $PATH — the wrapper the
#      plugin's hooks call (sr-session-hook.sh) gives the same install command
#      back if this step was skipped, so this is not the only place a stranger
#      hears it
#
# Deliberately NOT `go install ./services/...`: that is kept as a documented
# fallback for a contributor who already has Go, but it is not what this
# script does, because requiring a Go toolchain is the opposite of "a stranger
# gets guarded in about a minute".
set -eu

REPO="sloprail/sloprail"
INSTALL_DIR="${SLOPRAIL_INSTALL_DIR:-$HOME/.local/bin}"

say() { printf '%s\n' "$*"; }
die() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }

# --- platform detection ------------------------------------------------------
os="$(uname -s)"
case "$os" in
Darwin) os="darwin" ;;
Linux) os="linux" ;;
*) die "unsupported OS: $os (sloprail ships darwin and linux only — see 'go install ./services/...' in README.md for anything else)" ;;
esac

arch="$(uname -m)"
case "$arch" in
x86_64 | amd64) arch="amd64" ;;
arm64 | aarch64) arch="arm64" ;;
*) die "unsupported architecture: $arch (sloprail ships amd64 and arm64 only — see 'go install ./services/...' in README.md for anything else)" ;;
esac

platform="${os}-${arch}"
say "sloprail install: detected ${platform}"

# --- a10n-claude-mock (the agent double sr-test drives) ------------------------
# Pinned to internal/harnessmock/version.txt (what the sr-test code checks with
# --version). Released from sloprail/harness-mocks as per-platform raw binaries,
# a10n-claude-mock-<os>-<arch>, plus an optional checksums.txt.
# TODO: internal/harnessmock/version.txt is created by the sr-test Go part; until
# it is on this branch the fallback below is the single default.
HARNESS_MOCKS_REPO="sloprail/harness-mocks"
HARNESS_MOCK_DEFAULT_VERSION="0.2.0"

harness_mock_version() {
  v=""
  for f in "$(dirname "$0")/internal/harnessmock/version.txt" internal/harnessmock/version.txt; do
    [ -f "$f" ] && { v="$(tr -d ' \t\r\n' <"$f")"; break; }
  done
  v="${SLOPRAIL_HARNESS_MOCK_VERSION:-${v:-$HARNESS_MOCK_DEFAULT_VERSION}}"
  printf '%s' "${v#v}"
}

# install_harness_mock DIR: best-effort. sr-test is the only user, so a platform
# without a release asset warns instead of failing the sloprail install.
install_harness_mock() {
  hm_dir="$1"
  hm_ver="$(harness_mock_version)"
  hm_tag="v${hm_ver}"
  hm_asset="a10n-claude-mock-${os}-${arch}"
  hm_base="${SLOPRAIL_HARNESS_MOCK_URL:-https://github.com/${HARNESS_MOCKS_REPO}/releases/download/${hm_tag}}"
  hm_tmp="$(mktemp -d)"
  say "sloprail install: a10n-claude-mock ${hm_ver} (${hm_asset})"
  if ! fetch "${hm_base}/${hm_asset}" "${hm_tmp}/${hm_asset}" 2>/dev/null; then
    if [ -z "${SLOPRAIL_HARNESS_MOCK_URL:-}" ] && command -v gh >/dev/null 2>&1 &&
      gh release download "$hm_tag" --repo "$HARNESS_MOCKS_REPO" --pattern "$hm_asset" --dir "$hm_tmp" --clobber >/dev/null 2>&1; then
      :
    else
      say "sloprail install: WARNING — ${hm_base}/${hm_asset} is not available; a10n-claude-mock NOT installed (sr-test needs it; see https://github.com/${HARNESS_MOCKS_REPO}/releases)"
      rm -rf "$hm_tmp"
      return 0
    fi
  fi
  # Checksum: only when the release publishes one.
  if fetch "${hm_base}/checksums.txt" "${hm_tmp}/checksums.txt" 2>/dev/null; then
    hm_want="$(grep " ${hm_asset}\$" "${hm_tmp}/checksums.txt" | awk '{print $1}')"
    if [ -n "$hm_want" ]; then
      if command -v shasum >/dev/null 2>&1; then hm_got="$(shasum -a 256 "${hm_tmp}/${hm_asset}" | awk '{print $1}')"
      elif command -v sha256sum >/dev/null 2>&1; then hm_got="$(sha256sum "${hm_tmp}/${hm_asset}" | awk '{print $1}')"
      else hm_got="$hm_want"; fi
      [ "$hm_got" = "$hm_want" ] || die "checksum mismatch for ${hm_asset}: expected ${hm_want}, got ${hm_got} — not installing"
    fi
  else
    say "sloprail install: no checksums.txt published for ${hm_tag}; a10n-claude-mock is unverified"
  fi
  mkdir -p "$hm_dir"
  cp "${hm_tmp}/${hm_asset}" "${hm_dir}/a10n-claude-mock"
  chmod +x "${hm_dir}/a10n-claude-mock"
  command -v codesign >/dev/null 2>&1 && codesign --sign - --force "${hm_dir}/a10n-claude-mock" 2>/dev/null || true
  rm -rf "$hm_tmp"
  if got="$("${hm_dir}/a10n-claude-mock" --version 2>/dev/null)"; then
    case "$got" in
    *"$hm_ver"*) say "sloprail install: a10n-claude-mock ${hm_ver} verified into ${hm_dir}" ;;
    *) say "sloprail install: WARNING — a10n-claude-mock --version printed '${got}', expected ${hm_ver}" ;;
    esac
  else
    say "sloprail install: WARNING — could not run '${hm_dir}/a10n-claude-mock --version' (wrong platform, or this build has no --version)"
  fi
}


# --- downloader ---------------------------------------------------------------
# curl preferred, wget as the fallback every POSIX box that lacks curl usually
# has — this script has no other dependency Claude Code doesn't already need.
fetch() {
  # fetch URL DEST
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -q "$1" -O "$2"
  else
    die "neither curl nor wget is on \$PATH — install one and re-run"
  fi
}

# gh_ok: is an authenticated GitHub CLI available? The fallback for a release
# plain HTTP cannot see — while sloprail/sloprail is private, an anonymous
# curl of its release (or its "latest" redirect) is a 404, and the one
# credential a developer's machine reliably has for a private repo is gh's
# (or SSH, which serves git, not release assets). Tried only after the
# anonymous path fails, so a public release never needs gh at all.
gh_ok() {
  [ -z "${SLOPRAIL_RELEASE_URL:-}" ] || return 1 # an explicit source is never swapped for GitHub
  command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1
}

# --harness-mock-only: used by make distribute-local, installs just the mock.
if [ "${1:-}" = "--harness-mock-only" ]; then
  install_harness_mock "${2:-$INSTALL_DIR}"
  exit 0
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# Resolve "latest" to the actual tag BEFORE downloading, so this script (and
# the user) knows exactly what version landed rather than trusting a
# redirect silently — the tag is what plugin.json/marketplace.json are
# lockstepped with (scripts/bump-version.sh), so this is also what a user
# would cite when reporting a bug against a specific version.
#
# SLOPRAIL_INSTALL_TAG skips resolution entirely when already known — the
# stranger-install-path CI test sets it, since its local mock HTTP server
# (plain static files, no GitHub-shaped redirect) has no "latest" to
# resolve; a real user never sets it and always goes through the resolve
# step below.
if [ -n "${SLOPRAIL_INSTALL_TAG:-}" ]; then
  tag="$SLOPRAIL_INSTALL_TAG"
elif [ -n "${SLOPRAIL_RELEASE_URL:-}" ]; then
  tag="custom" # no GitHub "latest" to resolve for a release served elsewhere
else
  # github.com/OWNER/REPO/releases/latest is a redirect to
  # .../releases/tag/vX.Y.Z; curl -w reports the FINAL url it landed on
  # after following redirects (-L), which is where the resolved tag is read
  # from — no GitHub API call needed (the API's own /releases/latest needs
  # auth on a private repo; this plain redirect works the same either way
  # since it is the identical authenticated fetch the download itself uses).
  latest_url="https://github.com/${REPO}/releases/latest"
  resolved="$(curl -fsSL -o /dev/null -w '%{url_effective}' "$latest_url" 2>/dev/null || true)"
  tag="${resolved##*/tag/}"
  if { [ -z "$tag" ] || [ "$tag" = "$resolved" ]; } && gh_ok; then
    say "sloprail install: anonymous release lookup failed — resolving the latest tag through gh"
    tag="$(gh release view --repo "$REPO" --json tagName --jq .tagName 2>/dev/null || true)"
    resolved="gh:${tag}"
  fi
  if [ -z "$tag" ] || [ "$tag" = "$resolved" ]; then
    die "could not resolve the latest release tag from ${latest_url} (check https://github.com/${REPO}/releases exists and has at least one release)"
  fi
fi

# SLOPRAIL_RELEASE_URL points the download at another place holding the same
# files a release has (the platform archives + checksums.txt): a local mock
# server in CI, a file:// directory of archives built from a checkout in the
# onboarding eval. Nothing else changes — the checksum verify and install
# below run exactly as they do against GitHub.
base_url="${SLOPRAIL_RELEASE_URL:-https://github.com/${REPO}/releases/download/${tag}}"
archive="sloprail-${platform}.tar.gz"

# fetch_asset NAME: one release asset into $tmp — anonymously first, then
# through gh (see gh_ok) when the anonymous download is refused.
fetch_asset() {
  fetch "${base_url}/$1" "${tmp}/$1" 2>/dev/null && return 0
  gh_ok || return 1
  say "sloprail install: anonymous download of $1 failed — retrying through gh"
  gh release download "$tag" --repo "$REPO" --pattern "$1" --dir "$tmp" --clobber
}

say "sloprail install: downloading ${archive} from release ${tag}"
fetch_asset "${archive}" ||
  die "download failed — ${base_url}/${archive} (check https://github.com/${REPO}/releases for a build of your platform; for a private repo, log in with 'gh auth login' first)"

fetch_asset checksums.txt ||
  die "download failed — ${base_url}/checksums.txt"

# --- verify -------------------------------------------------------------------
want="$(grep " ${archive}\$" "${tmp}/checksums.txt" | awk '{print $1}')"
[ -n "$want" ] || die "checksums.txt has no entry for ${archive} — the release may be incomplete"

if command -v shasum >/dev/null 2>&1; then
  got="$(shasum -a 256 "${tmp}/${archive}" | awk '{print $1}')"
elif command -v sha256sum >/dev/null 2>&1; then
  got="$(sha256sum "${tmp}/${archive}" | awk '{print $1}')"
else
  say "sloprail install: WARNING — neither shasum nor sha256sum found; skipping checksum verification"
  got="$want"
fi

[ "$got" = "$want" ] || die "checksum mismatch for ${archive}: expected ${want}, got ${got} — download is corrupt or tampered, not installing"

# --- unpack and install ---------------------------------------------------------
tar -C "${tmp}" -xzf "${tmp}/${archive}"
mkdir -p "${INSTALL_DIR}"
# Fail before copying anything if the release lacks a binary this script installs (an older
# release predates one), instead of a bare `cp: cannot stat` halfway through.
missing=""
for bin in sr sr-session sr-file sr-mark sr-agent sr-eval sr-checks; do
  [ -f "${tmp}/sloprail-${platform}/${bin}" ] || missing="${missing} ${bin}"
done
[ -z "$missing" ] || die "release ${tag} does not contain:${missing} — it predates those binaries, nothing was installed. Install from source instead: GOBIN=\"${INSTALL_DIR}\" go install github.com/sloprail/sloprail/services/...@main"
for bin in sr sr-session sr-file sr-mark sr-agent sr-eval sr-checks; do
  cp "${tmp}/sloprail-${platform}/${bin}" "${INSTALL_DIR}/${bin}"
  chmod +x "${INSTALL_DIR}/${bin}"
  # macOS kills a binary copied over an existing signed one at exec with a
  # signature error that reads like anything but a signing problem (same
  # reasoning as the Makefile's distribute-local). Every copy is re-signed,
  # unconditionally, on every platform — codesign is a no-op non-error on Linux
  # hosts where it is simply absent.
  command -v codesign >/dev/null 2>&1 && codesign --sign - --force "${INSTALL_DIR}/${bin}" 2>/dev/null || true
done

install_harness_mock "${INSTALL_DIR}"

say "sloprail install: installed ${tag} (sr, sr-session, sr-file, sr-mark, sr-agent, sr-eval, sr-checks) into ${INSTALL_DIR}"

case ":$PATH:" in
*":${INSTALL_DIR}:"*) ;;
*)
  say
  say "NOTE: ${INSTALL_DIR} is not on your \$PATH."
  say "Add this to your shell profile, then start a new session:"
  say
  say "  export PATH=\"${INSTALL_DIR}:\$PATH\""
  say
  ;;
esac

say "Done. Verify with: sr-session start < /dev/null"
