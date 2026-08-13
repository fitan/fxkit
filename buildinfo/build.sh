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

# shellcheck disable=SC2046
eval $(BUILDINFO_DIR="$BUILDINFO_DIR" "$BUILDINFO_DIR/build-meta.sh")

BUILDINFO_PKG=${BUILDINFO_PKG:-github.com/fitan/fxkit/buildinfo}

LDFLAGS="-X '${BUILDINFO_PKG}.Version=${VERSION}' \
	-X '${BUILDINFO_PKG}.Commit=${COMMIT}' \
	-X '${BUILDINFO_PKG}.GitRemote=${GIT_REMOTE}' \
	-X '${BUILDINFO_PKG}.GitBranch=${GIT_BRANCH}' \
	-X '${BUILDINFO_PKG}.Dirty=${GIT_DIRTY}' \
	-X '${BUILDINFO_PKG}.GoVersion=${GO_VERSION}' \
	-X '${BUILDINFO_PKG}.BuildTime=${BUILD_TIME}' \
	-X '${BUILDINFO_PKG}.BuiltBy=${BUILT_BY}'"

mkdir -p .data "$(dirname "$OUT")"
# -buildvcs=true: embed vcs.revision/time as fallback when ldflags are missing.
go build -buildvcs=true -ldflags "$LDFLAGS" -o "$OUT" "$PKG"
