#!/bin/sh
# Install the latest orkestar release on macOS or Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/martintrifunov/orkestar/main/install.sh | sh
#
# A Homebrew tap covers people who use Homebrew. This is for everyone else,
# which is most people installing a terminal tool, and it is the first step
# where someone gives up if it is not there.
#
# POSIX sh on purpose: this runs before anything of ours is installed, on
# whatever the machine already has.

set -eu

REPOSITORY="martintrifunov/orkestar"
RELEASE_BASE="https://github.com/${REPOSITORY}/releases/latest/download"

fail() {
    printf 'orkestar: %s\n' "$1" >&2
    exit 1
}

need() {
    command -v "$1" >/dev/null 2>&1 || fail "this installer needs $1"
}

system=$(uname -s)
case "$system" in
    Darwin) platform=darwin ;;
    Linux) platform=linux ;;
    *) fail "unsupported operating system: $system. Windows has install.ps1." ;;
esac

machine=$(uname -m)
case "$machine" in
    x86_64 | amd64) architecture=amd64 ;;
    arm64 | aarch64) architecture=arm64 ;;
    *) fail "unsupported architecture: $machine" ;;
esac

archive="orkestar-${platform}-${architecture}.tar.gz"

# The install directory is chosen rather than assumed: a user-writable one is
# used without asking, and anything needing root says so instead of failing
# halfway through with a permissions error.
install_directory="${ORKESTAR_INSTALL_DIR:-}"
if [ -z "$install_directory" ]; then
    if [ -w "/usr/local/bin" ] 2>/dev/null; then
        install_directory="/usr/local/bin"
    else
        install_directory="${HOME}/.local/bin"
    fi
fi

need uname
need mkdir
need tar
if command -v curl >/dev/null 2>&1; then
    download() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
    download() { wget -qO "$2" "$1"; }
else
    fail "this installer needs curl or wget"
fi

if command -v shasum >/dev/null 2>&1; then
    checksum() { shasum -a 256 "$1" | cut -d' ' -f1; }
elif command -v sha256sum >/dev/null 2>&1; then
    checksum() { sha256sum "$1" | cut -d' ' -f1; }
else
    fail "this installer needs shasum or sha256sum to verify the download"
fi

work=$(mktemp -d 2>/dev/null || mktemp -d -t orkestar)
cleanup() { rm -rf "$work"; }
trap cleanup EXIT INT TERM

printf 'Downloading the latest orkestar release for %s %s...\n' "$platform" "$architecture"
download "${RELEASE_BASE}/${archive}" "${work}/${archive}" ||
    fail "could not download ${archive}"
download "${RELEASE_BASE}/SHA256SUMS" "${work}/SHA256SUMS" ||
    fail "could not download SHA256SUMS"

# Verified before anything is unpacked. An installer that runs a binary it did
# not check is not worth having.
expected=$(grep " ${archive}\$" "${work}/SHA256SUMS" | cut -d' ' -f1)
[ -n "$expected" ] || fail "the release checksum for ${archive} is missing"
actual=$(checksum "${work}/${archive}")
if [ "$expected" != "$actual" ]; then
    fail "the downloaded archive failed SHA-256 verification"
fi

tar -xzf "${work}/${archive}" -C "$work" || fail "could not unpack ${archive}"
[ -f "${work}/orkestar" ] || fail "the archive did not contain orkestar"

mkdir -p "$install_directory" || fail "could not create ${install_directory}"
if ! cp "${work}/orkestar" "${install_directory}/orkestar" 2>/dev/null; then
    fail "could not write to ${install_directory}. Set ORKESTAR_INSTALL_DIR to somewhere writable, or re-run with sudo."
fi
chmod 755 "${install_directory}/orkestar"

printf 'Installed orkestar to %s/orkestar\n' "$install_directory"
"${install_directory}/orkestar" --version || true

# Said rather than done: editing someone's shell profile behind their back is
# not a thing an installer should do.
case ":${PATH}:" in
    *":${install_directory}:"*) ;;
    *)
        printf '\n%s is not on your PATH. Add it with:\n\n' "$install_directory"
        printf '    export PATH="%s:$PATH"\n\n' "$install_directory"
        ;;
esac
