#!/usr/bin/env sh
# Build a Go binary with full buildinfo -ldflags (git + build time).
# Run from the application module root (directory containing go.mod).
#
# Usage:
#   ../fxkit/buildinfo/build.sh [-o bin/server] [./cmd]
#   ../fxkit/buildinfo/build.sh -C /path/to/example [-o bin/server] [./cmd]

set -e

BUILDINFO_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)
MODULE_ROOT=$(pwd)
OUT=bin/server
PKG=./cmd

while [ $# -gt 0 ]; do
	case "$1" in
	-C)
		MODULE_ROOT=$2
		shift 2
		;;
	-o)
		OUT=$2
		shift 2
		;;
	-h|--help)
		echo "usage: build.sh [-C module_root] [-o output] [package]" >&2
		exit 0
		;;
	-*)
		echo "build.sh: unknown option $1" >&2
		exit 2
		;;
	*)
		PKG=$1
		shift
		;;
	esac
done

cd "$MODULE_ROOT"

# 必须加引号：build-meta.sh 的输出以单引号包裹值，不加引号会被 shell 再次拆词。
eval "$(BUILDINFO_DIR="$BUILDINFO_DIR" "$BUILDINFO_DIR/build-meta.sh")"

BUILDINFO_PKG=${BUILDINFO_PKG:-github.com/fitan/fxkit/buildinfo}

# 转义为 Go ldflags 双引号内的值（先反斜杠再双引号）。
escape_ldflag() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

LDFLAGS="-X ${BUILDINFO_PKG}.Version=\"$(escape_ldflag "$VERSION")\" \
	-X ${BUILDINFO_PKG}.Commit=\"$(escape_ldflag "$COMMIT")\" \
	-X ${BUILDINFO_PKG}.GitRemote=\"$(escape_ldflag "$GIT_REMOTE")\" \
	-X ${BUILDINFO_PKG}.GitBranch=\"$(escape_ldflag "$GIT_BRANCH")\" \
	-X ${BUILDINFO_PKG}.Dirty=\"$(escape_ldflag "$GIT_DIRTY")\" \
	-X ${BUILDINFO_PKG}.GoVersion=\"$(escape_ldflag "$GO_VERSION")\" \
	-X ${BUILDINFO_PKG}.BuildTime=\"$(escape_ldflag "$BUILD_TIME")\" \
	-X ${BUILDINFO_PKG}.BuiltBy=\"$(escape_ldflag "$BUILT_BY")\""

mkdir -p .data "$(dirname "$OUT")"
# -buildvcs=true: embed vcs.revision/time as fallback when ldflags are missing.
go build -buildvcs=true -ldflags "$LDFLAGS" -o "$OUT" "$PKG"
