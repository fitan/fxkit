.PHONY: test test-race bench tidy build

GOPROXY ?= https://goproxy.cn,direct

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
