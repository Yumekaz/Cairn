#!/usr/bin/env bash
# Cairn installer: dependency check, DuraFlow sibling layout, build, install.

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m'

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo -e "${BLUE}=======================================${NC}"
echo -e "${BLUE}        Cairn Installation Script       ${NC}"
echo -e "${BLUE}=======================================${NC}"

echo -e "\nChecking dependencies..."

if ! command -v go >/dev/null 2>&1; then
  echo -e "${RED}Error: Go is not installed (need Go 1.22+).${NC}"
  exit 1
fi
echo -e "Found Go: $(go version | awk '{print $3}')"

if ! command -v python3 >/dev/null 2>&1; then
  echo -e "${YELLOW}Warning: python3 missing (required for Mini-Docker daemon).${NC}"
else
  echo -e "Found Python 3"
fi

# DuraFlow must be a sibling (or DURAFLOW_PATH) for the go.mod replace directive.
DURAFLOW_DIR="${DURAFLOW_PATH:-$ROOT/../DURAFLOW}"
if [[ ! -f "$DURAFLOW_DIR/go.mod" ]]; then
  echo -e "${RED}Error: DuraFlow not found at:${NC} $DURAFLOW_DIR"
  echo "Clone it next to Cairn (sibling layout):"
  echo "  git clone git@github.com:Yumekaz/DURAFLOW.git \"$ROOT/../DURAFLOW\""
  echo "Or set DURAFLOW_PATH and re-run:"
  echo "  DURAFLOW_PATH=/path/to/DURAFLOW ./scripts/install.sh"
  echo "  go mod edit -replace=github.com/yumekaz/duraflow=\"\$DURAFLOW_PATH\""
  exit 1
fi
# If user provided a non-default path, rewrite replace for this build.
if [[ "$(cd "$DURAFLOW_DIR" && pwd)" != "$(cd "$ROOT/../DURAFLOW" 2>/dev/null && pwd)" ]]; then
  echo -e "${YELLOW}Using DURAFLOW_PATH=$DURAFLOW_DIR${NC}"
  go mod edit -replace="github.com/yumekaz/duraflow=$DURAFLOW_DIR"
fi
echo -e "DuraFlow: $DURAFLOW_DIR"

MINI_DOCKER_DIR="${MINI_DOCKER_PATH:-$ROOT/../Mini-Docker}"
if [[ -d "$MINI_DOCKER_DIR/rootfs" ]]; then
  echo -e "Mini-Docker rootfs: $MINI_DOCKER_DIR/rootfs"
  if [[ -z "${CAIRN_ROOTFS:-}" ]]; then
    export CAIRN_ROOTFS="$MINI_DOCKER_DIR/rootfs"
  fi
else
  echo -e "${YELLOW}Mini-Docker not found at $MINI_DOCKER_DIR (optional for build; required to run).${NC}"
fi

echo -e "\nBuilding Cairn binaries..."
mkdir -p bin
go build -o bin/cairn ./cmd/cairn
go build -o bin/cairnd ./cmd/cairnd
echo -e "${GREEN}Binaries compiled under ./bin/${NC}"

echo -e "\nInitializing ~/.cairn ..."
CAIRN_DIR="${HOME}/.cairn"
mkdir -p "$CAIRN_DIR"/{volumes,backups/metadata,logs,services}

INSTALL_DIR="${HOME}/.local/bin"
if [[ "${EUID}" -eq 0 ]]; then
  INSTALL_DIR="/usr/local/bin"
fi
echo -e "\nInstalling binaries to ${INSTALL_DIR}..."
mkdir -p "$INSTALL_DIR"
cp bin/cairn "$INSTALL_DIR/cairn"
cp bin/cairnd "$INSTALL_DIR/cairnd"
echo -e "${GREEN}Installed to ${INSTALL_DIR}/${NC}"

echo -e "${BLUE}=======================================${NC}"
echo -e "${GREEN}Cairn is ready.${NC}"
echo "Next:"
echo "  export CAIRN_ROOTFS=\${CAIRN_ROOTFS:-$MINI_DOCKER_DIR/rootfs}"
echo "  export MINI_DOCKER_SOCKET=\${XDG_RUNTIME_DIR:-/run/user/\$(id -u)}/mini-docker/mini-docker.sock"
echo "  sudo python3 -m mini_docker daemon --socket \"\$MINI_DOCKER_SOCKET\" --socket-mode 666"
echo "  # PYTHONPATH must include Mini-Docker package root if not pip-installed"
echo "  cairn init && cairn doctor"
echo "  ./scripts/clean_demo.sh"
echo -e "${BLUE}=======================================${NC}"
