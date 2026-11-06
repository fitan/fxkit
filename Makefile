.PHONY: test test-race bench tidy build ci ci-full

GOPROXY ?= https://goproxy.cn,direct

# 与 CI 提供方无关的检查入口（GitHub / GitLab / Jenkins / 本地通用）。
# 快检：vet + gofmt + race 测试 + 覆盖率门禁 + example + 生成代码。
ci:
	bash scripts/ci.sh

# 追加：方言 / 集成测试 / atlas lint（缺环境变量会跳过并说明）。
ci-full:
	bash scripts/ci.sh --full

test:
	GOPROXY=$(GOPROXY) go test ./...

# -race 是本仓库最重要的一道闸（relay 心跳、锁续约、consul watch、生命周期并发）。
test-race:
	GOPROXY=$(GOPROXY) go test -race -count=1 ./...

# 热路径基线：路由匹配 / 日志 / 缓存。改动这些代码前后各跑一次。
bench:
	go test -run XXX -bench . -benchmem ./authz ./logx ./cachex ./crudx

tidy:
	GOPROXY=$(GOPROXY) go mod tidy

build:
	GOPROXY=$(GOPROXY) go build -o bin/fxkit ./cmd/fxkit
