#!/bin/sh
# Nexus dev setup — installs toolchain deps and runs the dev server.
# Usage: ./setup-dev.sh
#
# Targets macOS (Homebrew) and Linux (apt/dnf). Idempotent — safe to re-run.

set -e

REPO_ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$REPO_ROOT"

log()  { printf "\033[1;34m==>\033[0m %s\n" "$*"; }
warn() { printf "\033[1;33m!!\033[0m %s\n" "$*"; }
die()  { printf "\033[1;31mxx\033[0m %s\n" "$*"; exit 1; }

need_version() {
  # need_version <name> <found> <min>
  name=$1; found=$2; min=$3
  if [ -z "$found" ]; then
    return 1
  fi
  # Compare as dotted versions via sort -V
  lower=$(printf '%s\n%s\n' "$min" "$found" | sort -V | head -n1)
  [ "$lower" = "$min" ]
}

OS=$(uname -s)

install_mac() {
  if ! command -v brew >/dev/null 2>&1; then
    die "Homebrew not found. Install from https://brew.sh and re-run."
  fi
  log "Installing/updating Go and Node via Homebrew..."
  brew install go node >/dev/null
  if ! xcode-select -p >/dev/null 2>&1; then
    log "Installing Xcode Command Line Tools (GUI prompt)..."
    xcode-select --install || true
    warn "Finish the CLT install dialog, then re-run this script."
    exit 0
  fi
}

install_linux() {
  if command -v apt-get >/dev/null 2>&1; then
    log "Installing Go, Node, build tools via apt..."
    sudo apt-get update
    sudo apt-get install -y golang nodejs npm build-essential
  elif command -v dnf >/dev/null 2>&1; then
    log "Installing Go, Node, build tools via dnf..."
    sudo dnf install -y golang nodejs npm gcc gcc-c++ make
  else
    die "Unsupported Linux distro — install Go 1.25+, Node 22+, and gcc manually."
  fi
}

case "$OS" in
  Darwin) install_mac ;;
  Linux)  install_linux ;;
  *)      die "Unsupported OS: $OS" ;;
esac

# Verify versions
GO_VER=$(go version 2>/dev/null | awk '{print $3}' | sed 's/go//')
NODE_VER=$(node --version 2>/dev/null | sed 's/v//')

log "Go:   ${GO_VER:-missing}  (need >= 1.25)"
log "Node: ${NODE_VER:-missing} (need >= 22)"

need_version go   "$GO_VER"   "1.25" || die "Go 1.25+ required. Found: ${GO_VER:-none}"
need_version node "$NODE_VER" "22.0" || die "Node 22+ required. Found: ${NODE_VER:-none}"

# Fetch Go + Node deps
log "Fetching Go modules..."
go mod download

log "Installing web deps..."
npm --prefix web install

# Build + run
log "Building and starting Nexus dev server..."
exec make dev
