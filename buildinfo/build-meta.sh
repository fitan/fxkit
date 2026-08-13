#!/usr/bin/env sh
# Emit build metadata as shell assignments (source with: eval "$(./build-meta.sh)").
# Used by Next.js next.config and other non-Go build steps.

set -e

if git rev-parse --git-dir >/dev/null 2>&1; then
  VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
  GIT_REMOTE="${GIT_REMOTE:-$(git config --get remote.origin.url 2>/dev/null || echo unknown)}"
  GIT_BRANCH="${GIT_BRANCH:-$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown)}"
  COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
  if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
    GIT_DIRTY="${GIT_DIRTY:-true}"
  else
    GIT_DIRTY="${GIT_DIRTY:-false}"
  fi
else
  VERSION="${VERSION:-dev}"
  GIT_REMOTE="${GIT_REMOTE:-unknown}"
  GIT_BRANCH="${GIT_BRANCH:-unknown}"
  COMMIT="${COMMIT:-unknown}"
  GIT_DIRTY="${GIT_DIRTY:-false}"
fi

GO_VERSION="${GO_VERSION:-$(go version 2>/dev/null | awk '{print $3}' || echo unknown)}"
BUILD_TIME="${BUILD_TIME:-$(date -u +"%Y-%m-%dT%H:%M:%SZ")}"
BUILT_BY="${BUILT_BY:-$(whoami 2>/dev/null || echo unknown)}"

printf 'VERSION=%s\n' "$VERSION"
printf 'GIT_REMOTE=%s\n' "$GIT_REMOTE"
printf 'GIT_BRANCH=%s\n' "$GIT_BRANCH"
printf 'COMMIT=%s\n' "$COMMIT"
printf 'GIT_DIRTY=%s\n' "$GIT_DIRTY"
printf 'GO_VERSION=%s\n' "$GO_VERSION"
printf 'BUILD_TIME=%s\n' "$BUILD_TIME"
printf 'BUILT_BY=%s\n' "$BUILT_BY"
