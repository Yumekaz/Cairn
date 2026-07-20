#!/usr/bin/env bash
# Cairn installer: dependency check, DuraFlow sibling layout, build, install.
# Portable for stranger clone (no hardcoded /home/<user>/Desktop paths).

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

# Required Go version from go.mod (e.g. 1.26.4 → message 1.26+)
REQ_GO_FULL="$(awk '/^go / {print $2; exit}' go.mod 2>/dev/null || true)"
if [[ -z "$REQ_GO_FULL" ]]; then
  REQ_GO_FULL="1.26.4"
fi
REQ_GO_MINOR="$(echo "$REQ_GO_FULL" | awk -F. '{print $1"."$2}')"

echo -e "\nChecking dependencies..."

if ! command -v go >/dev/null 2>&1; then
  echo -e "${RED}Error: Go is not installed (need Go ${REQ_GO_MINOR}+ per go.mod; currently ${REQ_GO_FULL}).${NC}"
  echo "Install from https://go.dev/dl/ or enable GOTOOLCHAIN=auto with a recent toolchain."
  exit 1
fi
GO_VER_STR="$(go version 2>/dev/null || true)"
echo -e "Found Go: $(echo "$GO_VER_STR" | awk '{print $3}')"
echo -e "go.mod requires: go ${REQ_GO_FULL}"

# Best-effort version gate: compare major.minor (patch ignored)
INSTALLED_MM="$(echo "$GO_VER_STR" | sed -n 's/.*go\([0-9][0-9]*\.[0-9][0-9]*\).*/\1/p' | head -1)"
if [[ -n "$INSTALLED_MM" ]]; then
  # numeric compare of major*1000+minor
  req_maj="${REQ_GO_MINOR%%.*}"
  req_min="${REQ_GO_MINOR#*.}"
  inst_maj="${INSTALLED_MM%%.*}"
  inst_min="${INSTALLED_MM#*.}"
  if (( inst_maj * 1000 + inst_min < req_maj * 1000 + req_min )); then
    echo -e "${YELLOW}Warning: installed Go ${INSTALLED_MM} is older than go.mod ${REQ_GO_MINOR}+.${NC}"
    echo -e "${YELLOW}Build may still work if GOTOOLCHAIN=auto downloads ${REQ_GO_FULL}.${NC}"
  fi
fi

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
# Resolve to absolute path for replace if non-default layout
DURAFLOW_ABS="$(cd "$DURAFLOW_DIR" && pwd)"
SIBLING_ABS=""
if [[ -d "$ROOT/../DURAFLOW" ]]; then
  SIBLING_ABS="$(cd "$ROOT/../DURAFLOW" && pwd)"
fi
# If user provided a non-default path, rewrite replace for this build.
if [[ -n "$SIBLING_ABS" && "$DURAFLOW_ABS" != "$SIBLING_ABS" ]] || [[ -z "$SIBLING_ABS" && -n "${DURAFLOW_PATH:-}" ]]; then
  echo -e "${YELLOW}Using DURAFLOW_PATH=$DURAFLOW_ABS${NC}"
  go mod edit -replace="github.com/yumekaz/duraflow=$DURAFLOW_ABS"
fi
echo -e "DuraFlow: $DURAFLOW_ABS"

MINI_DOCKER_DIR="${MINI_DOCKER_PATH:-$ROOT/../Mini-Docker}"
if [[ -d "$MINI_DOCKER_DIR/rootfs" ]]; then
  MINI_DOCKER_ABS="$(cd "$MINI_DOCKER_DIR" && pwd)"
  echo -e "Mini-Docker rootfs: $MINI_DOCKER_ABS/rootfs"
  if [[ -z "${CAIRN_ROOTFS:-}" ]]; then
    export CAIRN_ROOTFS="$MINI_DOCKER_ABS/rootfs"
  fi
else
  echo -e "${YELLOW}Mini-Docker not found at $MINI_DOCKER_DIR (optional for build; required to run).${NC}"
  MINI_DOCKER_ABS="$MINI_DOCKER_DIR"
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

# Ensure ~/.local/bin is on PATH hint for strangers
if [[ "$INSTALL_DIR" == "${HOME}/.local/bin" ]]; then
  case ":${PATH}:" in
    *":${HOME}/.local/bin:"*) ;;
    *)
      echo -e "${YELLOW}Note: add ${HOME}/.local/bin to PATH if 'cairn' is not found:${NC}"
      echo "  export PATH=\"\${HOME}/.local/bin:\${PATH}\""
      ;;
  esac
fi

echo -e "${BLUE}=======================================${NC}"
echo -e "${GREEN}Cairn is ready.${NC}"
echo "Readiness check:  cairn init && cairn doctor"
echo "Full MLP proof:   ./scripts/prove_mlp.sh"
echo "Next (live runtime):"
echo "  export CAIRN_ROOTFS=\${CAIRN_ROOTFS:-$MINI_DOCKER_ABS/rootfs}"
echo "  export MINI_DOCKER_SOCKET=\${XDG_RUNTIME_DIR:-/run/user/\$(id -u)}/mini-docker/mini-docker.sock"
echo "  export PYTHONPATH=\${MINI_DOCKER_PATH:-$MINI_DOCKER_ABS}:\${PYTHONPATH:-}"
echo "  sudo env PYTHONPATH=\"\$PYTHONPATH\" python3 -m mini_docker daemon --socket \"\$MINI_DOCKER_SOCKET\" --socket-mode 666"
echo "  cairn init && cairn doctor"
echo "  ./scripts/clean_demo.sh"
echo -e "${BLUE}=======================================${NC}"
