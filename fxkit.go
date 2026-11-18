// Package fxkit 是一个轻量级脚手架，用于构建基于 Fx 的微服务：默认支持 HTTP（chi / 可选 Huma REST），
// 并可开箱即用集成 Hatchet（workflow / cron / events）与 GORM。
//
// 典型用法：
//
//	package main
//
//	import (
//	    "github.com/fitan/fxkit"
//	    "myapp/internal/hello"
//	)
//
//	func main() {
//	    fxkit.Run(fxkit.Default(), hello.Module)
//	}
//
// 框架采用按需启用：调用方可将 [Default] 换为 [Minimal]，或自行组合各子包的 Module。
package fxkit

import (
	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/buildinfo"
	"github.com/fitan/fxkit/cachex"
	"github.com/fitan/fxkit/cli"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/consulx"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/docs"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/hatchetx"
	"github.com/fitan/fxkit/mcpx"
	"github.com/fitan/fxkit/otelx"
	"github.com/fitan/fxkit/outbox"
	"github.com/fitan/fxkit/reqx"
	"github.com/fitan/fxkit/server"
)

// 重新导出共享的 Config 类型，调用方只需 import "fxkit"。
type Config = config.Config

// Core 是「无状态核心」：config、buildinfo、otelx、server（含 HTTP listener）与 cli。
//
// 不包含 docs / MCP / DB / authz / 服务发现 / outbox / workflow / cache ——
// 这些按需显式添加。适合只需要一个 HTTP 服务的最小场景。
func Core() di.Option {
	return di.Options(
		config.Module,
		buildinfo.Module,
		otelx.Module,
		server.Module,
		server.ListenerModule,
		cli.Module,
	)
}

// Default 打包常用 fxkit 子系统：Core + docs、gormx、hatchetx、outbox、authz、
// consulx、reqx、mcpx、cachex。
//
// 注意：docs 需要 `docs.enabled=true`、MCP 需要 `mcp.enabled=true` 才会对外暴露
// 端点（两者默认关闭）；其余子系统在未配置时大多为无操作。
func Default() di.Option {
	return di.Options(
		Core(),
		docs.Module,
		gormx.Module,
		hatchetx.Module,
		outbox.Module,
		authz.Module,
		consulx.Module,
		reqx.Module,
		mcpx.Module,
		cachex.Module,
	)
}

// Minimal 是 Core 的保留别名。脚手架的 --minimal 用这个名字。
func Minimal() di.Option {
	return Core()
}

// Data 是数据面服务预设：HTTP + gormx/outbox/cachex + 发现/出站 HTTP。
// 不含 authz/MCP/hatchet，适合内部数据 API 或 BFF。
func Data() di.Option {
	return di.Options(
		Core(),
		docs.Module,
		gormx.Module,
		outbox.Module,
		cachex.Module,
		consulx.Module,
		reqx.Module,
	)
}

// Worker 是无 HTTP listener 的后台任务预设：config/otel/cli + gormx + outbox + hatchet + cachex。
// `serve` 会直接等待退出信号，不绑定端口。
func Worker() di.Option {
	return di.Options(
		config.Module,
		buildinfo.Module,
		otelx.Module,
		cli.Module,
		gormx.Module,
		outbox.Module,
		hatchetx.Module,
		cachex.Module,
	)
}

// Run 将给定 options 装配为 Fx 应用并调用 Cobra 入口。
func Run(opts ...di.Option) {
	cli.Run(opts...)
}

// Option 重新导出 di.Option
type Option = di.Option
