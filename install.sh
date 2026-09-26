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
#   3. unpacks sr, sr-session, sr-file, sr-mark, sr-agent into one directory —
#      $SLOPRAIL_INSTALL_DIR if set, else ~/.local/bin — because sibling
#      resolution (internal/subbin) requires the set to be installed together
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
  command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1
}

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

base_url="https://github.com/${REPO}/releases/download/${tag}"
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
for bin in sr sr-session sr-file sr-mark sr-agent; do
  cp "${tmp}/sloprail-${platform}/${bin}" "${INSTALL_DIR}/${bin}"
  chmod +x "${INSTALL_DIR}/${bin}"
  # macOS kills a binary copied over an existing signed one at exec with a
  # signature error that reads like anything but a signing problem (same
  # reasoning as the Makefile's distribute-local). Every copy is re-signed,
  # unconditionally, on every platform — codesign is a no-op non-error on Linux
  # hosts where it is simply absent.
  command -v codesign >/dev/null 2>&1 && codesign --sign - --force "${INSTALL_DIR}/${bin}" 2>/dev/null || true
done

say "sloprail install: installed ${tag} (sr, sr-session, sr-file, sr-mark, sr-agent) into ${INSTALL_DIR}"

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
say
say "Next, if you have not yet: install the Claude Code plugin in your project,"
say "then start a new session (plugins load at session start):"
say
say "  claude plugin marketplace add sloprail/sloprail"
say "  claude plugin install sloprail@sloprail-marketplace --scope project"
