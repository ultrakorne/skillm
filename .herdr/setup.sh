#!/usr/bin/env bash
# .herdr/setup.sh — provision a fresh Go worktree. Linux and macOS.
#
# Run with CWD = the new worktree. `wt` (herdr-kit/new-worktree.sh) does this for
# you, or run it by hand right after `herdr worktree create` / `git worktree add`.
#
# This is the ONE place per-repo worktree setup lives — the generic launcher holds
# nothing project-specific and calls straight into here. A Go worktree needs very
# little: the module and build caches are per-user ($GOMODCACHE, $GOCACHE), not
# per-checkout, so every worktree already shares them. What doesn't follow
# `git worktree add` is gitignored local config, so we copy that from main.
#
# Provisioning is best-effort: a step that fails warns on stderr and leaves the
# worktree usable rather than aborting it.
set -euo pipefail

# Idempotency is OURS to decide: every step below is safe to re-run (copies never
# clobber, `go mod download` is a no-op on a warm cache), so there's no sentinel.

# Gitignored files that live only in the main checkout and must be copied into a
# fresh worktree. Paths are relative to the repo root. A missing source is skipped.
FILES_TO_COPY=(".env" ".env.local" ".mcp.json" ".claude/settings.local.json")

MAIN="$(git worktree list --porcelain | awk 'NR==1{print $2}')"   # main checkout path
[ -n "$MAIN" ] && [ -d "$MAIN" ] || { echo "setup: could not resolve main checkout" >&2; exit 1; }
WT="$(pwd)"

# 1) Copy gitignored local config from main (never clobber).
for rel in "${FILES_TO_COPY[@]}"; do
  [ -f "$MAIN/$rel" ] && [ ! -e "$WT/$rel" ] || continue
  mkdir -p "$(dirname "$WT/$rel")"
  cp "$MAIN/$rel" "$WT/$rel" && echo "  - copied $rel" \
    || echo "WARN: could not copy $rel" >&2
done

# 2) Warm the shared module cache from go.sum so the first build/test and gopls
#    start immediately. Usually a no-op — main has already fetched everything.
if command -v go >/dev/null 2>&1; then
  go mod download || echo "WARN: go mod download failed; modules will fetch on first build." >&2
else
  echo "WARN: go not on PATH; skipping module download." >&2
fi

echo "worktree provisioned: $WT"
