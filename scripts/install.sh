#!/usr/bin/env bash

# Cairn Installer Script
# Verifies dependencies, builds binaries, and initializes system directories.

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}=======================================${NC}"
echo -e "${BLUE}        Cairn Installation Script       ${NC}"
echo -e "${BLUE}=======================================${NC}"

# 1. Dependency checks
echo -e "\nChecking dependencies..."

if ! command -v go >/dev/null 2>&1; then
    echo -e "${RED}Error: Go (Golang) is not installed. Please install Go v1.22+ to build Cairn.${NC}"
    exit 1
fi

GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
echo -e "Found Go: v${GO_VERSION}"

if ! command -v python3 >/dev/null 2>&1; then
    echo -e "${RED}Warning: python3 is not installed. Python 3 is required to run the default Mini-Docker runtime daemon.${NC}"
else
    echo -e "Found Python 3"
fi

# 2. Build binaries
echo -e "\nBuilding Cairn binaries..."
mkdir -p bin

echo "Compiling CLI client (cairn)..."
go build -o bin/cairn ./cmd/cairn

echo "Compiling Control plane daemon (cairnd)..."
go build -o bin/cairnd ./cmd/cairnd

echo -e "${GREEN}Binaries compiled successfully under ./bin/${NC}"

# 3. Create Cairn system directories
echo -e "\nInitializing Cairn home directory (~/.cairn)..."
CAIRN_DIR="$HOME/.cairn"

mkdir -p "$CAIRN_DIR"
mkdir -p "$CAIRN_DIR/volumes"
mkdir -p "$CAIRN_DIR/backups"
mkdir -p "$CAIRN_DIR/backups/metadata"
mkdir -p "$CAIRN_DIR/logs"
mkdir -p "$CAIRN_DIR/services"

echo -e "Directories initialized at ${CAIRN_DIR}"

# 4. Install binaries
INSTALL_DIR="$HOME/.local/bin"
if [ "$EUID" -eq 0 ]; then
    INSTALL_DIR="/usr/local/bin"
fi

echo -e "\nInstalling binaries to ${INSTALL_DIR}..."
mkdir -p "$INSTALL_DIR"
cp bin/cairn "$INSTALL_DIR/cairn"
cp bin/cairnd "$INSTALL_DIR/cairnd"

echo -e "${GREEN}Cairn installed successfully to ${INSTALL_DIR}/${NC}"

# 5. Instructions
echo -e "${BLUE}=======================================${NC}"
echo -e "${GREEN}Cairn is now ready to use!${NC}"
echo -e "Ensure ${INSTALL_DIR} is in your PATH."
echo -e "\nNext steps:"
echo -e "1. Run '${GREEN}cairn init${NC}' to initialize the SQLite database."
echo -e "2. Start the daemon with '${GREEN}cairnd${NC}' or '${GREEN}cairn daemon start${NC}'."
echo -e "3. Deploy your first app using '${GREEN}cairn deploy examples/counter-api/cairn.yaml${NC}'."
echo -e "${BLUE}=======================================${NC}"
