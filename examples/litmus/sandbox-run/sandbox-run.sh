#!/usr/bin/env bash
#
# Reference runner for the litmus example.
#
# It launches an isolated opencode session for a single actor so the Phase 5
# disposability experiment stays reproducible. It is not the product path: a
# real coder runs in its own service directory and connects to boundary-mcp
# over MCP.
#
# Usage:
#   sandbox-run <actor_name> [opencode arguments...]
#
# boundary-mcp runs OUTSIDE the sandbox and opencode connects to it over
# loopback Streamable HTTP. governance/ and the other actors are never
# mounted into the sandbox.
#
# Environment:
#   BOUNDARY_ROOT       example root (default: examples/litmus)
#   BOUNDARY_MCP_BIN    boundary-mcp binary (default: <repo>/bin/boundary-mcp)
#   BOUNDARY_MCP_ADDR   HTTP listen address (default: 127.0.0.1:0 = ephemeral port)
#   BOUNDARY_DRY_RUN    when non-empty, print the plan and exit
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
Usage: sandbox-run <actor_name> [opencode arguments...]

Environment:
  BOUNDARY_ROOT      example root (default: examples/litmus)
  BOUNDARY_MCP_BIN   override the boundary-mcp binary
  BOUNDARY_MCP_ADDR  override the HTTP listen address
  BOUNDARY_DRY_RUN   print the plan instead of launching the sandbox
EOF
}

if [[ $# -lt 1 ]]; then
  usage
  exit 2
fi

ACTOR=$1
shift

if [[ -z "${HOME:-}" ]]; then
  echo "sandbox-run: HOME is not set" >&2
  exit 1
fi

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "$SCRIPT_DIR/../../.." && pwd)
ROOT_DIR=${BOUNDARY_ROOT:-$(cd -- "$SCRIPT_DIR/.." && pwd)}
ACTOR_DIR="$ROOT_DIR/actors/$ACTOR"

if [[ ! -d "$ACTOR_DIR" ]]; then
  echo "sandbox-run: actor directory not found: $ACTOR_DIR" >&2
  exit 1
fi

if [[ ! -f "$ROOT_DIR/governance/purpose.md" || ! -f "$ROOT_DIR/governance/protocol.fbs" ]]; then
  echo "sandbox-run: governance files missing under $ROOT_DIR/governance" >&2
  exit 1
fi

BOUNDARY_MCP_BIN=${BOUNDARY_MCP_BIN:-"$REPO_ROOT/bin/boundary-mcp"}
if [[ ! -x "$BOUNDARY_MCP_BIN" ]]; then
  echo "sandbox-run: boundary-mcp binary not found: $BOUNDARY_MCP_BIN" >&2
  echo "             build it with: make build" >&2
  exit 1
fi

STATE_DIR="$ROOT_DIR/.sandbox/$ACTOR"
mkdir -p "$STATE_DIR"
RUN_DIR=$(mktemp -d "$STATE_DIR/run.XXXXXX")
READY_FILE="$RUN_DIR/ready"
LOG_FILE="$RUN_DIR/boundary-mcp.log"
CONFIG="$RUN_DIR/opencode.json"

MCP_PID=""
cleanup() {
  if [[ -n "$MCP_PID" ]] && kill -0 "$MCP_PID" 2>/dev/null; then
    kill "$MCP_PID" 2>/dev/null || true
    wait "$MCP_PID" 2>/dev/null || true
  fi
  # bwrap creates an empty mount point for the read-only opencode.json.
  if [[ -f "$ACTOR_DIR/opencode.json" && ! -s "$ACTOR_DIR/opencode.json" ]]; then
    rm -f "$ACTOR_DIR/opencode.json"
  fi
  rm -rf "$RUN_DIR"
}
trap cleanup EXIT

TOKEN=$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')

if [[ -n "${BOUNDARY_DRY_RUN:-}" ]]; then
  MCP_URL="http://127.0.0.1:0/mcp"
else
  "$BOUNDARY_MCP_BIN" \
    -transport http \
    -addr "${BOUNDARY_MCP_ADDR:-127.0.0.1:0}" \
    -actor-token "$ACTOR=$TOKEN" \
    -governance "$ROOT_DIR/governance" \
    -sdk /sdk \
    -sdk-out "$ROOT_DIR/sdk" \
    -ready-file "$READY_FILE" \
    >"$LOG_FILE" 2>&1 &
  MCP_PID=$!

  for _ in $(seq 1 200); do
    if [[ -s "$READY_FILE" ]]; then
      break
    fi
    if ! kill -0 "$MCP_PID" 2>/dev/null; then
      echo "sandbox-run: boundary-mcp exited before becoming ready" >&2
      cat "$LOG_FILE" >&2 || true
      exit 1
    fi
    sleep 0.05
  done

  if [[ ! -s "$READY_FILE" ]]; then
    echo "sandbox-run: timed out waiting for boundary-mcp" >&2
    cat "$LOG_FILE" >&2 || true
    exit 1
  fi

  MCP_URL=$(<"$READY_FILE")
fi

sed -e "s|__BOUNDARY_MCP_URL__|$MCP_URL|g" \
    -e "s|__BOUNDARY_MCP_TOKEN__|$TOKEN|g" \
    "$SCRIPT_DIR/opencode.json.tmpl" > "$CONFIG"

ARGS=(
  --die-with-parent
  --proc /proc
  --dev /dev
  --tmpfs /tmp
  --chdir /workspace
  --unshare-pid
)

add_ro() {
  if [[ -e "$1" ]]; then
    ARGS+=(--ro-bind "$1" "$2")
  fi
}

add_rw() {
  if [[ -e "$1" ]]; then
    ARGS+=(--bind "$1" "$2")
  fi
}

add_ro /usr /usr
add_ro /bin /bin
add_ro /sbin /sbin
add_ro /lib /lib
add_ro /lib64 /lib64
add_ro /etc/resolv.conf /etc/resolv.conf
add_ro /etc/ssl /etc/ssl
add_ro /etc/passwd /etc/passwd
add_ro /etc/group /etc/group
add_ro /etc/nsswitch.conf /etc/nsswitch.conf
add_ro /etc/localtime /etc/localtime

CONFIG_HOME=${XDG_CONFIG_HOME:-"$HOME/.config"}
DATA_HOME=${XDG_DATA_HOME:-"$HOME/.local/share"}
STATE_HOME=${XDG_STATE_HOME:-"$HOME/.local/state"}
CACHE_HOME=${XDG_CACHE_HOME:-"$HOME/.cache"}

mkdir -p "$DATA_HOME/opencode" "$STATE_HOME/opencode" "$CACHE_HOME/opencode"

add_ro "$CONFIG_HOME/opencode" "$CONFIG_HOME/opencode"
add_rw "$DATA_HOME/opencode" "$DATA_HOME/opencode"
add_rw "$STATE_HOME/opencode" "$STATE_HOME/opencode"
add_rw "$CACHE_HOME/opencode" "$CACHE_HOME/opencode"

# Go toolchain caches: the agent must be able to build from the read-only SDK
# and resolve the FlatBuffers module without re-downloading it on every run.
GO_PATH=$(go env GOPATH 2>/dev/null || true)
if [[ -n "$GO_PATH" ]]; then
  mkdir -p "$GO_PATH"
  add_rw "$GO_PATH" "$GO_PATH"
fi
GO_CACHE=$(go env GOCACHE 2>/dev/null || true)
if [[ -n "$GO_CACHE" ]]; then
  mkdir -p "$GO_CACHE"
  add_rw "$GO_CACHE" "$GO_CACHE"
fi

add_ro "$ROOT_DIR/sdk" /sdk

ARGS+=(
  --bind "$ACTOR_DIR" /workspace
  --ro-bind "$CONFIG" /workspace/opencode.json
)

# `run` is an opencode subcommand whose flags must follow it; the default TUI
# keeps --standalone as a top-level flag.
if [[ "${1:-}" == "run" ]]; then
  shift
  ARGS+=(opencode run --standalone)
else
  ARGS+=(opencode --standalone)
fi

if [[ -n "${BOUNDARY_DRY_RUN:-}" ]]; then
  echo "boundary-mcp URL: $MCP_URL"
  echo "workspace:        $ACTOR_DIR -> /workspace (read-write)"
  echo "sdk:              $ROOT_DIR/sdk -> /sdk (read-only)"
  echo "opencode config:"
  sed 's/^/  /' "$CONFIG"
  echo "bwrap:"
  printf '  %q' bwrap "${ARGS[@]}" "$@"
  printf '\n'
  exit 0
fi

bwrap "${ARGS[@]}" "$@"
