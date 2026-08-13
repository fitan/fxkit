.PHONY: test tidy build

GOPROXY ?= https://goproxy.cn,direct

test:
	GOPROXY=$(GOPROXY) go test ./...

tidy:
	GOPROXY=$(GOPROXY) go mod tidy

build:
	GOPROXY=$(GOPROXY) go build -o bin/fxkit ./cmd/fxkit
