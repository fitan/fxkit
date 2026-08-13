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
	"github.com/fitan/fxkit/cli"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/consulx"
	"github.com/fitan/fxkit/docs"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/hatchetx"
	"github.com/fitan/fxkit/otelx"
	"github.com/fitan/fxkit/outbox"
	"github.com/fitan/fxkit/reqx"
	"github.com/fitan/fxkit/server"
	"go.uber.org/fx"
)

// 重新导出共享的 Config 类型，调用方只需 import "fxkit"。
type Config = config.Config

// Default 打包常用 fxkit 子系统：config、buildinfo、otelx、server（含 HTTP listener）、
// docs、gormx、hatchetx、outbox、authz、consulx、reqx 与 cli。
//
// 不再包含 Dapr；HTTP 由 [server.ListenerModule] 直接监听。
func Default() fx.Option {
	return fx.Options(
		config.Module,
		buildinfo.Module,
		otelx.Module,
		server.Module,
		server.ListenerModule,
		docs.Module,
		gormx.Module,
		hatchetx.Module,
		outbox.Module,
		authz.Module,
		consulx.Module,
		reqx.Module,
		cli.Module,
	)
}

// Minimal 是最小可用组合：config、buildinfo、server（含独立 HTTP listener）、docs 与 cli。
func Minimal() fx.Option {
	return fx.Options(
		config.Module,
		buildinfo.Module,
		server.Module,
		server.ListenerModule,
		docs.Module,
		cli.Module,
	)
}

// Run 将给定 options 装配为 Fx 应用并调用 Cobra 入口。
func Run(opts ...fx.Option) {
	cli.Run(opts...)
}
