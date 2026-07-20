#!/usr/bin/env bash
# bootstrap_stack.sh — one-shot stranger path for the Cairn spine.
#
# What this IS:
#   Clone missing siblings (Cairn/SERVER + DURAFLOW + Mini-Docker), fix go.mod
#   replace to ../DURAFLOW, run install.sh, init, optional Mini-Docker + cairnd,
#   then doctor. Intended for a single Linux host.
#
# What this is NOT:
#   Multi-node setup, FailForge campaigns, publishing DuraFlow as a module,
#   cloud provisioners, or rootless Mini-Docker hardening.
#
# Usage:
#   # From empty parent directory (or any dir):
#   ./bootstrap_stack.sh --parent ~/src/cairn-stack
#
#   # From inside an existing Cairn clone (recommended after git clone Cairn):
#   cd Cairn && ./scripts/bootstrap_stack.sh
#   ./scripts/bootstrap_stack.sh --start-runtime   # needs sudo for Mini-Docker
#
# Env:
#   CAIRN_REMOTE / DURAFLOW_REMOTE / MINI_DOCKER_REMOTE — override clone URLs
#   START_RUNTIME=1 — same as --start-runtime
#   SUDO_PASSWORD — non-interactive sudo (never written to files; match runtime.sh)
#   DURAFLOW_PATH / MINI_DOCKER_PATH — non-sibling layouts (install.sh / runtime.sh)
#
# Exit: 0 if install succeeded (runtime may still need manual start);
#       non-zero on clone/install failure.

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m'

log()  { echo -e "${BLUE}[bootstrap]${NC} $*"; }
ok()   { echo -e "${GREEN}[bootstrap]${NC} $*"; }
warn() { echo -e "${YELLOW}[bootstrap]${NC} $*"; }
# Named fail (not die) so scripts/lib/runtime.sh can keep its own die/_cairn_runtime_die.
fail() { echo -e "${RED}[bootstrap] ERROR:${NC} $*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Usage: bootstrap_stack.sh [options]

  --parent DIR       Parent directory for sibling checkouts (Cairn, DURAFLOW, Mini-Docker)
  --start-runtime    Start Mini-Docker + cairnd (needs root / sudo -n / SUDO_PASSWORD)
  --https            Use HTTPS git remotes (default)
  --ssh              Use git@ GitHub remotes
  -h, --help         Show this help

From empty parent:
  ./bootstrap_stack.sh --parent ~/src/cairn-stack

From existing Cairn/SERVER clone:
  cd Cairn && ./scripts/bootstrap_stack.sh
  ./scripts/bootstrap_stack.sh --start-runtime

Env overrides: CAIRN_REMOTE, DURAFLOW_REMOTE, MINI_DOCKER_REMOTE, START_RUNTIME=1, SUDO_PASSWORD
EOF
}

# --- flags ---
PARENT_FLAG=""
START_RUNTIME="${START_RUNTIME:-0}"
USE_SSH=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --parent)
      [[ $# -ge 2 ]] || fail "--parent requires a directory argument"
      PARENT_FLAG="$2"
      shift 2
      ;;
    --start-runtime)
      START_RUNTIME=1
      shift
      ;;
    --https)
      USE_SSH=0
      shift
      ;;
    --ssh)
      USE_SSH=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unknown argument: $1 (try -h)"
      ;;
  esac
done

# --- resolve script / repo context ---
SCRIPT_PATH="$(readlink -f "${BASH_SOURCE[0]}" 2>/dev/null || true)"
if [[ -z "$SCRIPT_PATH" ]]; then
  SCRIPT_PATH="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"
fi
SCRIPT_DIR="$(cd "$(dirname "$SCRIPT_PATH")" && pwd)"
# scripts/ lives under repo root when this is the in-tree copy
SCRIPT_REPO=""
if [[ -f "${SCRIPT_DIR}/../go.mod" ]] && grep -q 'module github.com/yumekaz/cairn' "${SCRIPT_DIR}/../go.mod" 2>/dev/null; then
  SCRIPT_REPO="$(cd "${SCRIPT_DIR}/.." && pwd)"
fi

# Default remotes (HTTPS preferred for strangers)
if [[ "$USE_SSH" -eq 1 ]]; then
  DEFAULT_CAIRN="git@github.com:Yumekaz/Cairn.git"
  DEFAULT_DF="git@github.com:Yumekaz/DURAFLOW.git"
  DEFAULT_MD="git@github.com:Yumekaz/Mini-Docker.git"
else
  DEFAULT_CAIRN="https://github.com/Yumekaz/Cairn.git"
  DEFAULT_DF="https://github.com/Yumekaz/DURAFLOW.git"
  DEFAULT_MD="https://github.com/Yumekaz/Mini-Docker.git"
fi
CAIRN_REMOTE="${CAIRN_REMOTE:-$DEFAULT_CAIRN}"
DURAFLOW_REMOTE="${DURAFLOW_REMOTE:-$DEFAULT_DF}"
MINI_DOCKER_REMOTE="${MINI_DOCKER_REMOTE:-$DEFAULT_MD}"

# --- resolve PARENT and CAIRN_DIR ---
# Logic:
#   1. --parent DIR → PARENT=DIR; CAIRN_DIR = existing Cairn/SERVER under it, or clone as Cairn
#   2. Script lives in .../Cairn/scripts or .../SERVER/scripts → PARENT=repo parent, CAIRN_DIR=repo
#   3. Else PARENT=cwd
PARENT=""
CAIRN_DIR=""

if [[ -n "$PARENT_FLAG" ]]; then
  mkdir -p "$PARENT_FLAG"
  PARENT="$(cd "$PARENT_FLAG" && pwd)"
  if [[ -n "$SCRIPT_REPO" ]]; then
    # Running the in-tree script with --parent: if script repo is already under PARENT, use it
    script_parent="$(cd "$SCRIPT_REPO/.." && pwd)"
    if [[ "$script_parent" == "$PARENT" ]]; then
      CAIRN_DIR="$SCRIPT_REPO"
    fi
  fi
  if [[ -z "$CAIRN_DIR" ]]; then
    if [[ -f "$PARENT/Cairn/go.mod" ]] && grep -q 'module github.com/yumekaz/cairn' "$PARENT/Cairn/go.mod" 2>/dev/null; then
      CAIRN_DIR="$PARENT/Cairn"
    elif [[ -f "$PARENT/SERVER/go.mod" ]] && grep -q 'module github.com/yumekaz/cairn' "$PARENT/SERVER/go.mod" 2>/dev/null; then
      CAIRN_DIR="$PARENT/SERVER"
    else
      # Will clone as Cairn under PARENT
      CAIRN_DIR="$PARENT/Cairn"
    fi
  fi
elif [[ -n "$SCRIPT_REPO" ]]; then
  CAIRN_DIR="$SCRIPT_REPO"
  PARENT="$(cd "$CAIRN_DIR/.." && pwd)"
else
  PARENT="$(pwd)"
  if [[ -f "$PARENT/Cairn/go.mod" ]] && grep -q 'module github.com/yumekaz/cairn' "$PARENT/Cairn/go.mod" 2>/dev/null; then
    CAIRN_DIR="$PARENT/Cairn"
  elif [[ -f "$PARENT/SERVER/go.mod" ]] && grep -q 'module github.com/yumekaz/cairn' "$PARENT/SERVER/go.mod" 2>/dev/null; then
    CAIRN_DIR="$PARENT/SERVER"
  elif [[ -f "$PARENT/go.mod" ]] && grep -q 'module github.com/yumekaz/cairn' "$PARENT/go.mod" 2>/dev/null; then
    CAIRN_DIR="$PARENT"
    PARENT="$(cd "$CAIRN_DIR/.." && pwd)"
  else
    CAIRN_DIR="$PARENT/Cairn"
  fi
fi

log "PARENT=$PARENT"
log "CAIRN_DIR=$CAIRN_DIR"

# --- clone helpers ---
clone_if_missing() {
  local dest="$1"
  local remote="$2"
  local label="$3"
  if [[ -d "$dest/.git" ]] || [[ -f "$dest/go.mod" ]] || [[ -d "$dest/mini_docker" ]] || [[ -d "$dest/rootfs" ]]; then
    ok "skip clone $label (already present at $dest)"
    return 0
  fi
  if [[ -e "$dest" ]]; then
    fail "$dest exists but does not look like a usable $label checkout"
  fi
  log "Cloning $label → $dest"
  log "  remote: $remote"
  git clone --depth 1 "$remote" "$dest" || fail "failed to clone $label from $remote"
  ok "cloned $label"
}

# Cairn: only clone if missing (never re-clone existing repo / SERVER checkout)
if [[ ! -f "$CAIRN_DIR/go.mod" ]] || ! grep -q 'module github.com/yumekaz/cairn' "$CAIRN_DIR/go.mod" 2>/dev/null; then
  clone_if_missing "$CAIRN_DIR" "$CAIRN_REMOTE" "Cairn"
else
  ok "skip clone Cairn (already present at $CAIRN_DIR)"
fi

clone_if_missing "$PARENT/DURAFLOW" "$DURAFLOW_REMOTE" "DURAFLOW"
clone_if_missing "$PARENT/Mini-Docker" "$MINI_DOCKER_REMOTE" "Mini-Docker"

# Sanity: siblings exist relative to Cairn root
[[ -f "$CAIRN_DIR/go.mod" ]] || fail "Cairn go.mod missing at $CAIRN_DIR"
[[ -f "$PARENT/DURAFLOW/go.mod" ]] || fail "DURAFLOW missing at $PARENT/DURAFLOW"
[[ -d "$PARENT/Mini-Docker" ]] || fail "Mini-Docker missing at $PARENT/Mini-Docker"

# go.mod replace is relative to Cairn root: ../DURAFLOW must resolve
REL_DF="$(cd "$CAIRN_DIR/.." && pwd)/DURAFLOW"
ABS_DF="$(cd "$PARENT/DURAFLOW" && pwd)"
if [[ "$REL_DF" != "$ABS_DF" ]]; then
  fail "DURAFLOW is not a sibling of Cairn ($CAIRN_DIR). Expected $REL_DF, found $ABS_DF. go.mod replace => ../DURAFLOW requires siblings."
fi

# --- cd to Cairn / SERVER root ---
cd "$CAIRN_DIR"
ROOT="$CAIRN_DIR"
export ROOT

# --- ensure go.mod replace is ../DURAFLOW (not absolute /home) ---
if grep -E 'replace .*duraflow => /' go.mod >/dev/null 2>&1; then
  warn "go.mod replace used an absolute path; rewriting to ../DURAFLOW"
  go mod edit -replace="github.com/yumekaz/duraflow=../DURAFLOW"
fi
if ! grep -q 'replace github.com/yumekaz/duraflow => ../DURAFLOW' go.mod; then
  log "Setting go.mod replace → ../DURAFLOW"
  go mod edit -replace="github.com/yumekaz/duraflow=../DURAFLOW"
fi
ok "go.mod replace: $(grep 'replace github.com/yumekaz/duraflow' go.mod || true)"

# --- install ---
[[ -x "$ROOT/scripts/install.sh" ]] || chmod +x "$ROOT/scripts/install.sh"
log "Running ./scripts/install.sh"
./scripts/install.sh

# Prefer built + installed binaries
export PATH="${ROOT}/bin:${HOME}/.local/bin:${PATH}"
if [[ -d /usr/local/bin ]]; then
  export PATH="/usr/local/bin:${PATH}"
fi

# --- discover / export runtime env ---
# shellcheck source=lib/runtime.sh
source "${SCRIPT_DIR}/lib/runtime.sh"
cairn_runtime_discover

if [[ -z "${CAIRN_ROOTFS:-}" && -d "$PARENT/Mini-Docker/rootfs" ]]; then
  export CAIRN_ROOTFS="$(cd "$PARENT/Mini-Docker/rootfs" && pwd)"
fi
if [[ -d "$PARENT/Mini-Docker" ]]; then
  md_abs="$(cd "$PARENT/Mini-Docker" && pwd)"
  case ":${PYTHONPATH:-}:" in
    *":${md_abs}:"*) ;;
    *) export PYTHONPATH="${md_abs}${PYTHONPATH:+:$PYTHONPATH}" ;;
  esac
fi

echo ""
log "Runtime env (export these in new shells):"
echo "  export CAIRN_ROOTFS=\"${CAIRN_ROOTFS:-$PARENT/Mini-Docker/rootfs}\""
echo "  export PYTHONPATH=\"${PYTHONPATH:-$PARENT/Mini-Docker}\""
echo "  export MINI_DOCKER_SOCKET=\"${MINI_DOCKER_SOCKET:-${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/mini-docker/mini-docker.sock}\""
echo "  export PATH=\"${ROOT}/bin:\${HOME}/.local/bin:\${PATH}\""
echo ""

# --- privileges banner ---
echo -e "${YELLOW}────────────────────────────────────────────────────────────${NC}"
echo -e "${YELLOW}Privileges / Mini-Docker (host-managed networking)${NC}"
echo "  • Linux only (namespaces, OverlayFS, bridge networking)"
echo "  • Starting Mini-Docker needs root, passwordless sudo (sudo -n),"
echo "    or non-interactive SUDO_PASSWORD in the environment"
echo "  • This script never hangs on an interactive sudo password prompt"
echo "  • Never commit passwords; pass SUDO_PASSWORD only in the local shell"
echo -e "${YELLOW}────────────────────────────────────────────────────────────${NC}"
echo ""

# --- cairn init ---
if command -v cairn >/dev/null 2>&1; then
  log "cairn init"
  cairn init || warn "cairn init returned non-zero (continuing)"
else
  fail "cairn binary not found on PATH after install (checked ${ROOT}/bin and ~/.local/bin)"
fi

RUNTIME_STARTED=0
DOCTOR_OK=0

if [[ "$START_RUNTIME" == "1" ]]; then
  log "START_RUNTIME: ensure Mini-Docker + cairnd (via scripts/lib/runtime.sh)"
  # Subshell: runtime.sh die/exit must not abort bootstrap after a successful install.
  # Needs root, passwordless sudo (-n), or SUDO_PASSWORD (never hangs on TTY prompt).
  if (
    set -euo pipefail
    ensure_minidocker
    ensure_cairnd
    wait_doctor 30
  ); then
    RUNTIME_STARTED=1
    DOCTOR_OK=1
  else
    warn "runtime start or doctor failed — need root, passwordless sudo (-n), or SUDO_PASSWORD for Mini-Docker"
    warn "build/install OK; start runtime next"
    # Best-effort: if processes came up anyway, note it
    if command -v cairn >/dev/null 2>&1 && cairn status >/dev/null 2>&1 \
      && [[ -S "${MINI_DOCKER_SOCKET:-}" ]]; then
      RUNTIME_STARTED=1
    fi
  fi
else
  cat <<EOF
Next steps (start runtime manually):

  # Env (if not already exported above)
  export CAIRN_ROOTFS="\${CAIRN_ROOTFS:-$PARENT/Mini-Docker/rootfs}"
  export PYTHONPATH="\${PYTHONPATH:-$PARENT/Mini-Docker}"
  export MINI_DOCKER_SOCKET="\${MINI_DOCKER_SOCKET:-${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/mini-docker/mini-docker.sock}"
  export PATH="${ROOT}/bin:\${HOME}/.local/bin:\${PATH}"

  # Mini-Docker (requires sudo/root)
  sudo mkdir -p "\$(dirname "\$MINI_DOCKER_SOCKET")"
  sudo env PYTHONPATH="\$PYTHONPATH" python3 -m mini_docker daemon \\
    --socket "\$MINI_DOCKER_SOCKET" \\
    --socket-mode 666

  # Or non-interactive: SUDO_PASSWORD='…' ./scripts/bootstrap_stack.sh --start-runtime
  # Or from this repo after install: source scripts/lib/runtime.sh && ensure_minidocker && ensure_cairnd

  cairn daemon start   # or: ensure_cairnd via runtime.sh
  cairn doctor
  ./scripts/prove_mlp.sh   # optional full MLP proof
EOF
fi

# Always try doctor if cairnd + md look available
if [[ "$RUNTIME_STARTED" -eq 0 ]]; then
  if command -v cairn >/dev/null 2>&1; then
    if cairn status >/dev/null 2>&1 && [[ -S "${MINI_DOCKER_SOCKET:-}" ]]; then
      log "cairnd + Mini-Docker socket present — running cairn doctor"
      if MINI_DOCKER_SOCKET="${MINI_DOCKER_SOCKET:-}" cairn doctor; then
        DOCTOR_OK=1
      else
        warn "cairn doctor failed (runtime unhealthy?)"
      fi
    else
      warn "build/install OK; start runtime next, then: cairn doctor"
    fi
  fi
else
  if [[ "$DOCTOR_OK" -eq 1 ]]; then
    log "Final cairn doctor"
    MINI_DOCKER_SOCKET="${MINI_DOCKER_SOCKET:-}" cairn doctor || true
  else
    warn "build OK; doctor not green — check Mini-Docker / cairnd logs"
  fi
fi

echo ""
if [[ "$DOCTOR_OK" -eq 1 ]]; then
  ok "Bootstrap complete — install + runtime healthy (cairn doctor OK)."
else
  ok "Bootstrap complete — install succeeded."
  if [[ "$START_RUNTIME" != "1" ]]; then
    log "Runtime not started (pass --start-runtime or START_RUNTIME=1 when sudo is available)."
  fi
fi
# Exit 0 if install succeeded; clone/install failures already exited earlier.
exit 0
