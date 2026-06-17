#!/bin/sh
# berrypick installer for macOS and Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/fverse/berrypick/main/install.sh | sh
#
# Environment variables:
#   VERSION      release tag to install (default: latest)
#   INSTALL_DIR  where to put the binary (default: /usr/local/bin)
set -eu

REPO="fverse/berrypick"
BINARY="berrypick"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# Detect OS.
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
	linux | darwin) ;;
	*)
		echo "berrypick: unsupported OS: $os" >&2
		exit 1
		;;
esac

# Detect architecture.
arch=$(uname -m)
case "$arch" in
	x86_64 | amd64) arch="amd64" ;;
	aarch64 | arm64) arch="arm64" ;;
	*)
		echo "berrypick: unsupported architecture: $arch" >&2
		exit 1
		;;
esac

# Resolve the version (latest by default).
version="${VERSION:-}"
if [ -z "$version" ]; then
	version=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" |
		grep '"tag_name":' | head -n1 | sed -E 's/.*"([^"]+)".*/\1/')
fi
if [ -z "$version" ]; then
	echo "berrypick: could not determine the latest version" >&2
	exit 1
fi

asset="${BINARY}_${version}_${os}_${arch}.tar.gz"
url="https://github.com/${REPO}/releases/download/${version}/${asset}"

echo "Installing ${BINARY} ${version} (${os}/${arch})..."

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$url" | tar -xz -C "$tmp"

if [ -w "$INSTALL_DIR" ]; then
	mv "$tmp/${BINARY}" "$INSTALL_DIR/${BINARY}"
else
	echo "Writing to ${INSTALL_DIR} (may prompt for sudo)..."
	sudo mv "$tmp/${BINARY}" "$INSTALL_DIR/${BINARY}"
fi

echo "Installed ${BINARY} to ${INSTALL_DIR}/${BINARY}"
"${INSTALL_DIR}/${BINARY}" --version 2>/dev/null || true
