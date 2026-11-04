.PHONY: help build run tidy clean verify verify-local openapi gen-client gen-web edge edge-down down

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
	@echo "make verify-local      # SQLite 单机端到端冒烟（不需要 Docker/Postgres）"
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

# 不依赖 Docker/Postgres/Consul/Hatchet 的端到端冒烟（SQLite）。
verify-local:
	bash scripts/verify-local.sh

# 配置与装配自检（不监听端口，可在 CI 中运行）
#
# storefront 的 example 配置开了 auth.enabled 但没配 issuer/audience
# （它依赖 dev_header_user + 下游服务鉴权），validate 会如实报错，
# 因此这里只对 catalog 做 --strict 校验，storefront 仅报告。
#
# --di-graph 需要能完成 PreStart（casbin 需要数据库），所以用自包含的
# sqlite 临时配置，而不是依赖 postgres 的开发配置。
SELFCHECK_CFG := /tmp/fxkit-selfcheck.yaml

$(SELFCHECK_CFG):
	@printf 'app:\n  name: catalog\nserver:\n  port: "18099"\ndb:\n  driver: "sqlite"\n  dsn: "/tmp/fxkit-selfcheck.db"\n' > $(SELFCHECK_CFG)

check: build $(SELFCHECK_CFG)
	@echo "== config validate (catalog, strict) =="
	./bin/catalog validate --config services/catalog/configs/config.yaml --strict
	@echo "== config validate (storefront, report only) =="
	-./bin/storefront validate --config services/storefront/configs/config.yaml
	@echo "== DI graph sanity (catalog) =="
	rm -f /tmp/fxkit-selfcheck.db
	./bin/catalog serve --config $(SELFCHECK_CFG) --di-graph > /tmp/fxkit-di.txt 2>/dev/null
	@! grep -q EMPTY /tmp/fxkit-di.txt || { echo "empty DI group detected"; cat /tmp/fxkit-di.txt; exit 1; }
	@grep -qE '^  server\.Route +[1-9]' /tmp/fxkit-di.txt \
		|| { echo "server.Route group is empty"; cat /tmp/fxkit-di.txt; exit 1; }
	@echo "DI graph ok"
	@echo "== DI graph sanity (storefront) =="
	./bin/storefront serve --config $(SELFCHECK_CFG) --di-graph > /tmp/fxkit-di-sf.txt 2>/dev/null
	@grep -qE '^  server\.Route +[1-9]' /tmp/fxkit-di-sf.txt \
		|| { echo "storefront wiring broken"; cat /tmp/fxkit-di-sf.txt; exit 1; }
	@echo "storefront DI graph ok"
