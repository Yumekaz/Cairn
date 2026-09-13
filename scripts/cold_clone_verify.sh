#!/usr/bin/env bash
# Private cold-clone self-check: clone Cairn + DURAFLOW + Mini-Docker into a
# fresh directory, build, test, and run clean_demo. No friends required.
# Portable: default COLD_DIR is under $TMPDIR (not ~/Desktop).
#
# Usage:
#   ./scripts/cold_clone_verify.sh
#   COLD_DIR=/tmp/cairn-cold ./scripts/cold_clone_verify.sh
#   SKIP_DEMO=1 ./scripts/cold_clone_verify.sh   # build+unit only

set -euo pipefail

REPO_REMOTE_CAIRN="${CAIRN_REMOTE:-git@github.com:Yumekaz/Cairn.git}"
REPO_REMOTE_DF="${DURAFLOW_REMOTE:-git@github.com:Yumekaz/DURAFLOW.git}"
REPO_REMOTE_MD="${MINI_DOCKER_REMOTE:-git@github.com:Yumekaz/Mini-Docker.git}"
# Prefer TMPDIR (or /tmp) so headless/stranger machines without ~/Desktop work
COLD_DIR="${COLD_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/cairn-cold-clone.XXXXXX")}"
SOURCE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CAIRN_REV="${CAIRN_REV:-$(git -C "$SOURCE_DIR" rev-parse HEAD)}"
SKIP_DEMO="${SKIP_DEMO:-0}"

log() { echo "[cold-clone] $*"; }
die() { echo "[cold-clone] ERROR: $*" >&2; exit 1; }

[[ ! -e "$COLD_DIR" || ( -d "$COLD_DIR" && -z "$(ls -A "$COLD_DIR")" ) ]] || die "COLD_DIR must be new or empty; existing content is never deleted"
log "Using fresh directory $COLD_DIR"
mkdir -p "$COLD_DIR"
cd "$COLD_DIR"

log "Cloning repos (sibling layout)"
git clone --depth 1 "$REPO_REMOTE_CAIRN" Cairn
git clone --depth 1 "$REPO_REMOTE_DF" DURAFLOW
git clone --depth 1 "$REPO_REMOTE_MD" Mini-Docker
git -C Cairn fetch --depth 1 origin "$CAIRN_REV"
git -C Cairn checkout --detach "$CAIRN_REV"
while read -r name revision; do
  [[ -z "$name" || "$name" == \#* ]] && continue
  case "$name" in DURAFLOW|Mini-Docker) ;; *) die "unknown locked repository $name";; esac
  [[ "$revision" =~ ^[0-9a-f]{40}$ ]] || die "invalid locked revision for $name"
  git -C "$name" fetch --depth 1 origin "$revision"
  git -C "$name" checkout --detach "$revision"
done < Cairn/stack.lock

cd "$COLD_DIR/Cairn"
bash scripts/verify_stack.sh

# Sanity: replace must be sibling, not a hardcoded home path
if grep -E 'replace .*duraflow => /home/' go.mod; then
  die "go.mod still hardcodes a home path for duraflow"
fi
if ! grep -q 'replace github.com/yumekaz/duraflow => ../DURAFLOW' go.mod; then
  log "WARN: expected replace => ../DURAFLOW; found:"
  grep replace go.mod || true
fi

# Surface go.mod toolchain for stranger debug
if grep -q '^go ' go.mod; then
  log "go.mod: $(awk '/^go /{print; exit}' go.mod)"
fi

log "Building via install.sh"
chmod +x scripts/install.sh
./scripts/install.sh

export PATH="$COLD_DIR/Cairn/bin:${HOME}/.local/bin:${PATH}"
export CAIRN_ROOTFS="$COLD_DIR/Mini-Docker/rootfs"
export MINI_DOCKER_SOCKET="${MINI_DOCKER_SOCKET:-${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/mini-docker/mini-docker.sock}"
export PYTHONPATH="$COLD_DIR/Mini-Docker${PYTHONPATH:+:$PYTHONPATH}"
export MINI_DOCKER_PYTHON="${MINI_DOCKER_PYTHON:-$(command -v python3)}"


log "Unit tests"
go test ./internal/deploymeta/ ./internal/daemon/ ./internal/config/ ./internal/preflight/ ./internal/store/ -count=1

if [[ "$SKIP_DEMO" == "1" ]]; then
  log "SKIP_DEMO=1 — build+tests only. OK."
  exit 0
fi

[[ -x "$CAIRN_ROOTFS/bin/busybox" || -x "$CAIRN_ROOTFS/bin/sh" ]] || die "rootfs incomplete at $CAIRN_ROOTFS"

log "Running clean_demo.sh (uses existing Mini-Docker socket if present)"
chmod +x scripts/clean_demo.sh
# Prefer cold-built binaries
export PATH="$COLD_DIR/Cairn/bin:$PATH"
# Stop any foreign cairnd so demo starts the cold binary
cairn daemon stop 2>/dev/null || true
sleep 0.5

bash scripts/clean_demo.sh 2>&1 | tee "$COLD_DIR/cold_demo.log"
log "COLD CLONE VERIFY PASSED"
log "Tree: $COLD_DIR"
log "Log:  $COLD_DIR/cold_demo.log"
