# Shared -ldflags for github.com/fitan/fxkit/buildinfo.
# Prefer [build.sh] from app Makefiles and Air (sets metadata in a real shell).
# Or include here and: go build -ldflags "$(GO_LDFLAGS)" -o bin/server ./cmd

BUILDINFO_PKG ?= github.com/fitan/fxkit/buildinfo

# Resolve git root from this file's location (works when make -C example/ …).
_BUILDINFO_DIR := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))
BUILDINFO_GIT_DIR ?= $(or $(shell git -C "$(CURDIR)" rev-parse --show-toplevel 2>/dev/null),$(shell git -C "$(_BUILDINFO_DIR)" rev-parse --show-toplevel 2>/dev/null))

VERSION ?= $(shell git -C "$(BUILDINFO_GIT_DIR)" describe --tags --always --dirty 2>/dev/null || echo dev)
GIT_REMOTE ?= $(shell git -C "$(BUILDINFO_GIT_DIR)" config --get remote.origin.url 2>/dev/null || echo unknown)
GIT_BRANCH ?= $(shell git -C "$(BUILDINFO_GIT_DIR)" rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown)
COMMIT ?= $(shell git -C "$(BUILDINFO_GIT_DIR)" rev-parse --short HEAD 2>/dev/null || echo unknown)
GIT_DIRTY ?= $(shell test -n "$$(git -C "$(BUILDINFO_GIT_DIR)" status --porcelain 2>/dev/null)" && echo true || echo false)
GO_VERSION ?= $(shell go version 2>/dev/null | awk '{print $$3}' || echo unknown)
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
BUILT_BY ?= $(shell whoami 2>/dev/null || echo unknown)

# Quote each -X value so URLs and spaces survive shell/make expansion.
GO_LDFLAGS := -X '$(BUILDINFO_PKG).Version=$(VERSION)' \
	-X '$(BUILDINFO_PKG).Commit=$(COMMIT)' \
	-X '$(BUILDINFO_PKG).GitRemote=$(GIT_REMOTE)' \
	-X '$(BUILDINFO_PKG).GitBranch=$(GIT_BRANCH)' \
	-X '$(BUILDINFO_PKG).Dirty=$(GIT_DIRTY)' \
	-X '$(BUILDINFO_PKG).GoVersion=$(GO_VERSION)' \
	-X '$(BUILDINFO_PKG).BuildTime=$(BUILD_TIME)' \
	-X '$(BUILDINFO_PKG).BuiltBy=$(BUILT_BY)'
