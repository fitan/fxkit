.PHONY: help build run tidy clean verify openapi gen-client gen-web edge edge-down down

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo showcase)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
GO_VERSION ?= $(shell go version 2>/dev/null | awk '{print $$3}' || echo unknown)
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

export GOPROXY ?= https://goproxy.cn,direct

GO_LDFLAGS := -X 'github.com/fitan/fxkit/buildinfo.Version=$(VERSION)' \
	-X 'github.com/fitan/fxkit/buildinfo.Commit=$(COMMIT)' \
	-X 'github.com/fitan/fxkit/buildinfo.GoVersion=$(GO_VERSION)' \
	-X 'github.com/fitan/fxkit/buildinfo.BuildTime=$(BUILD_TIME)'

SERVICES := $(patsubst services/%/cmd,%,$(wildcard services/*/cmd))

help:
	@echo "make tidy              # go mod tidy"
	@echo "make build             # build catalog + storefront into bin/"
	@echo "make run SVC=catalog   # run one service"
	@echo "make openapi SVC=catalog"
	@echo "make gen-client        # fxkit gen client from services/catalog/openapi.yaml"
	@echo "make gen-web           # OpenAPI → web/src/*.d.ts + bundle app.js"
	@echo "make edge              # docker compose: Traefik 3.7.12 (static + Consul)"
	@echo "make edge-down"
	@echo "make verify            # start services, probe, then leave them running"
	@echo "make down              # stop catalog/storefront + Traefik"

build:
	@test -n "$(SERVICES)" || (echo "no services"; exit 1)
	@for s in $(SERVICES); do \
		echo "building $$s"; \
		go build -ldflags "$(GO_LDFLAGS)" -o bin/$$s ./services/$$s/cmd; \
	done

run:
	@test -n "$(SVC)" || (echo "usage: make run SVC=catalog|storefront"; exit 1)
	go run -ldflags "$(GO_LDFLAGS)" ./services/$(SVC)/cmd serve --config services/$(SVC)/configs/config.yaml

openapi:
	@test -n "$(SVC)" || (echo "usage: make openapi SVC=catalog"; exit 1)
	go run -ldflags "$(GO_LDFLAGS)" ./services/$(SVC)/cmd openapi --config services/$(SVC)/configs/config.yaml -o services/$(SVC)/openapi.yaml

gen-client:
	@test -f services/catalog/openapi.yaml || (echo "run: make openapi SVC=catalog"; exit 1)
	go run ../cmd/fxkit gen client --spec services/catalog/openapi.yaml --out services/storefront/internal/clients/catalog

gen-web:
	@test -f services/catalog/openapi.yaml || (echo "run: make openapi SVC=catalog"; exit 1)
	@test -f services/storefront/openapi.yaml || (echo "run: make openapi SVC=storefront"; exit 1)
	cd web && npm run gen && npm run check && npm run build

edge:
	docker compose up -d --remove-orphans

edge-down:
	docker compose down

down: edge-down
	-pkill -f '$(CURDIR)/bin/catalog serve'
	-pkill -f '$(CURDIR)/bin/storefront serve'

tidy:
	go mod tidy

clean:
	rm -rf bin/ tmp/

verify: tidy build
	bash scripts/verify.sh
