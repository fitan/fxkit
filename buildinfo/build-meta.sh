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

# 输出经过 shell 引号转义的赋值，使 `eval "$(./build-meta.sh)"` 在值包含空格、
# 引号、$ 或反引号（例如 git remote URL）时也不会被二次解析/注入。
quote() {
  printf '%s' "$1" | sed "s/'/'\\\\''/g"
}
emit() {
  printf "%s='%s'\n" "$1" "$(quote "$2")"
}

emit VERSION "$VERSION"
emit GIT_REMOTE "$GIT_REMOTE"
emit GIT_BRANCH "$GIT_BRANCH"
emit COMMIT "$COMMIT"
emit GIT_DIRTY "$GIT_DIRTY"
emit GO_VERSION "$GO_VERSION"
emit BUILD_TIME "$BUILD_TIME"
emit BUILT_BY "$BUILT_BY"
