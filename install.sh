#!/bin/sh
# Nexus installer — downloads the latest release binary
# Usage: curl -fsSL https://raw.githubusercontent.com/vortex-303/nexus/main/install.sh | sh

set -e

REPO="vortex-303/nexus"
INSTALL_DIR="/usr/local/bin"

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  linux)  OS="linux" ;;
  darwin) OS="darwin" ;;
  *)      echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64)  ARCH="amd64" ;;
  aarch64|arm64)  ARCH="arm64" ;;
  *)              echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

echo "Detected: ${OS}/${ARCH}"

# Get latest release tag
echo "Fetching latest release..."
LATEST=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
if [ -z "$LATEST" ]; then
  echo "Error: could not determine latest release"
  exit 1
fi
VERSION="${LATEST#v}"
echo "Latest version: ${VERSION}"

# Download
FILENAME="nexus_${VERSION}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${LATEST}/${FILENAME}"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

echo "Downloading ${URL}..."
curl -fsSL -o "${TMPDIR}/${FILENAME}" "$URL"

# Verify checksum if sha256sum is available
CHECKSUMS_URL="https://github.com/${REPO}/releases/download/${LATEST}/checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
  curl -fsSL -o "${TMPDIR}/checksums.txt" "$CHECKSUMS_URL"
  (cd "$TMPDIR" && grep "$FILENAME" checksums.txt | sha256sum -c --quiet)
  echo "Checksum verified"
elif command -v shasum >/dev/null 2>&1; then
  curl -fsSL -o "${TMPDIR}/checksums.txt" "$CHECKSUMS_URL"
  (cd "$TMPDIR" && grep "$FILENAME" checksums.txt | shasum -a 256 -c --quiet)
  echo "Checksum verified"
fi

# Extract
tar -xzf "${TMPDIR}/${FILENAME}" -C "$TMPDIR"

# Install
if [ -w "$INSTALL_DIR" ]; then
  mv "${TMPDIR}/nexus" "${INSTALL_DIR}/nexus"
else
  echo "Installing to ${INSTALL_DIR} (requires sudo)..."
  sudo mv "${TMPDIR}/nexus" "${INSTALL_DIR}/nexus"
fi

chmod +x "${INSTALL_DIR}/nexus"
echo ""
echo "nexus ${VERSION} installed to ${INSTALL_DIR}/nexus"
echo ""
echo "Get started:"
echo "  nexus serve"
echo ""
echo "Then open http://localhost:8080 in your browser."
echo ""
