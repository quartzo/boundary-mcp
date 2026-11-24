#!/usr/bin/env bash
#
# Destructive Phase 5b test:
#   1. wipe actor_enrich,
#   2. launch a sandboxed opencode session with the reconstruction prompt,
#   3. run the hidden suites,
#   4. restore the previous implementation on failure.
#
# Usage:
#   CONFIRM=1 ./examples/litmus/scripts/disposability.sh
#
# Environment:
#   BOUNDARY_MCP_BIN  override the boundary-mcp binary
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
LITMUS_ROOT=$(cd -- "$SCRIPT_DIR/.." && pwd)
REPO_ROOT=$(cd -- "$LITMUS_ROOT/../.." && pwd)

ACTOR=actor_enrich
ACTOR_DIR="$LITMUS_ROOT/actors/$ACTOR"
PROMPT="$LITMUS_ROOT/prompts/reconstruct_actor_enrich.md"
MCP_BIN=${BOUNDARY_MCP_BIN:-"$REPO_ROOT/bin/boundary-mcp"}

if [[ "${CONFIRM:-}" != "1" ]]; then
  echo "This wipes $ACTOR_DIR and launches an agent to reconstruct it." >&2
  echo "Re-run with CONFIRM=1 to proceed." >&2
  exit 2
fi

if [[ ! -x "$MCP_BIN" ]]; then
  echo "Building boundary-mcp..." >&2
  make -C "$REPO_ROOT" build >/dev/null
fi

if ! git -C "$REPO_ROOT" diff --quiet -- "$ACTOR_DIR"; then
  echo "Uncommitted changes in $ACTOR_DIR; commit or stash them first." >&2
  exit 1
fi

BACKUP=$(mktemp -d /tmp/opencode/litmus-actor.XXXXXX)
cp -a "$ACTOR_DIR/." "$BACKUP/"

restore=1
cleanup() {
  if [[ "$restore" == "1" ]]; then
    echo "Restoring the previous $ACTOR implementation..." >&2
    rm -rf "$ACTOR_DIR"
    mkdir -p "$ACTOR_DIR"
    cp -a "$BACKUP/." "$ACTOR_DIR/"
  fi
  rm -rf "$BACKUP"
}
trap cleanup EXIT

echo "Wiping $ACTOR_DIR..."
rm -rf "$ACTOR_DIR"
mkdir -p "$ACTOR_DIR"
cp "$PROMPT" "$ACTOR_DIR/PROMPT.md"

echo "Launching the sandboxed agent..."
BOUNDARY_ROOT="$LITMUS_ROOT" \
BOUNDARY_MCP_BIN="$MCP_BIN" \
  "$REPO_ROOT/cmd/sandbox-run/sandbox-run.sh" "$ACTOR" \
    --auto run "Implement the task in /workspace/PROMPT.md. Use the boundary MCP tools. When you are done, call verify_actor_behavior for actor_enrich."

echo "Verifying the reconstructed actor..."
if (cd "$REPO_ROOT" && go run ./examples/litmus/verify -root examples/litmus); then
  restore=0
  echo "Disposability test passed; keeping the reconstructed implementation."
else
  echo "Disposability test failed; restoring the previous implementation." >&2
  exit 1
fi
