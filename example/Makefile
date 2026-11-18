.PHONY: help build run tidy clean check verify verify-local openapi gen-client gen-web web-install web-dev web-dev-direct web-check demo demo-down edge edge-down down atlas-status atlas-lint atlas-apply migrate-baseline

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
	@echo "make gen-client        # fxkit gen client from services/catalog/api/openapi.yaml"
	@echo "make gen-web           # OpenAPI → web/app/types/{catalog,storefront}.d.ts"
	@echo "make web-install       # pnpm install for web/"
	@echo "make web-dev           # Nuxt dev :3000，API 经 Traefik :8880（先 make demo）"
	@echo "make web-dev-direct    # Nuxt dev :3000，API 直连 8081/8082"
	@echo "make web-check         # Nuxt typecheck + lint"
	@echo "make demo              # SQLite catalog + storefront for the dashboard"
	@echo "make demo-down         # stop the SQLite demo services"
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
	go run -ldflags "$(GO_LDFLAGS)" ./services/$(SVC)/cmd openapi --config services/$(SVC)/configs/config.yaml -o services/$(SVC)/api/openapi.yaml

gen-client:
	@test -f services/catalog/api/openapi.yaml || (echo "run: make openapi SVC=catalog"; exit 1)
	go run ../cmd/fxkit gen client --spec services/catalog/api/openapi.yaml --out services/storefront/internal/clients/catalog

gen-web:
	@test -f services/catalog/api/openapi.yaml || (echo "run: make openapi SVC=catalog"; exit 1)
	@test -f services/storefront/api/openapi.yaml || (echo "run: make openapi SVC=storefront"; exit 1)
	cd web && corepack pnpm gen

web-install:
	cd web && corepack pnpm install

web-dev:
	cd web && NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost NUXT_CATALOG_BASE=http://127.0.0.1:8880/catalog NUXT_STOREFRONT_BASE=http://127.0.0.1:8880/storefront corepack pnpm dev --host 0.0.0.0 --port 3000

web-dev-direct:
	cd web && NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost corepack pnpm dev --host 0.0.0.0 --port 3000

web-check:
	cd web && corepack pnpm typecheck && corepack pnpm lint

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

demo:
	bash scripts/demo-local.sh

demo-down:
	-pkill -f 'catalog serve -c .*catalog-demo.yaml'
	-pkill -f 'storefront serve -c .*storefront-demo.yaml'
	-docker compose stop consul traefik redis

# Atlas 迁移（需要安装 atlas CLI：https://atlasgo.io）
atlas-status:
	atlas migrate status --env local

atlas-lint:
	atlas migrate lint --env local --latest 1

atlas-apply:
	atlas migrate apply --env local

# 把 fxkit 框架表写成第一份迁移；之后业务迁移追加到同一目录
migrate-baseline: build
	./bin/catalog migrate baseline --dialect postgres --dir migrations/postgres

verify: tidy build
	bash scripts/verify.sh

# 不依赖 Docker/Postgres/Consul/Hatchet 的端到端冒烟（SQLite）。
verify-local:
	bash scripts/verify-local.sh

# 配置与装配自检（不监听端口，可在 CI 中运行）
#
# storefront 用 dev_header_user + casbin（不配 issuer/audience），
# 两个服务都做 --strict 校验。
#
# --di-graph 需要能完成 PreStart（casbin 需要数据库），所以用自包含的
# sqlite 临时配置，而不是依赖 postgres 的开发配置。
SELFCHECK_CFG := /tmp/fxkit-selfcheck.yaml

$(SELFCHECK_CFG):
	@printf 'app:\n  name: catalog\nserver:\n  port: "18099"\ndb:\n  driver: "sqlite"\n  dsn: "/tmp/fxkit-selfcheck.db"\n' > $(SELFCHECK_CFG)

check: build $(SELFCHECK_CFG)
	@echo "== config validate (catalog, strict) =="
	./bin/catalog validate --config services/catalog/configs/config.yaml --strict
	@echo "== config validate (storefront, strict) =="
	./bin/storefront validate --config services/storefront/configs/config.yaml --strict
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
