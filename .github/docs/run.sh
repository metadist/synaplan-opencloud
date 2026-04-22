#!/usr/bin/env bash
# Local bootstrap for the docs build.
#
# Clones dschmidt/opencloud-service-docs-action at the commit SHA pinned in
# .github/workflows/docs.yml (single source of truth) and invokes its
# build.sh. Output and caching behaviour match what CI does exactly.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && cd .. && pwd)"
WORKFLOW="$REPO_ROOT/.github/workflows/docs.yml"
ACTION_REPO="dschmidt/opencloud-service-docs-action"
ACTION_DIR="$SCRIPT_DIR/.cache/action"

REF="$(grep -oE "${ACTION_REPO}@[^ \"']+" "$WORKFLOW" | head -1 | cut -d@ -f2)"
[ -n "$REF" ] || { echo "error: could not find ${ACTION_REPO}@<ref> in $WORKFLOW" >&2; exit 1; }

HAVE="$(git -C "$ACTION_DIR" rev-parse HEAD 2>/dev/null || true)"
if [ "$HAVE" != "$REF" ]; then
  echo "==> fetching ${ACTION_REPO}@${REF}"
  rm -rf "$ACTION_DIR"
  mkdir -p "$(dirname "$ACTION_DIR")"
  git clone "https://github.com/${ACTION_REPO}.git" "$ACTION_DIR"
  git -C "$ACTION_DIR" checkout "$REF"
fi

exec bash "$ACTION_DIR/build.sh"
