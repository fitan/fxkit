// Package stack 就是 fxkit.Default()，只是用 docs.MergedModule 替换了
// 占位的 Scalar 骨架。示例服务需要 /docs 上有实时的 Huma。
package stack

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/buildinfo"
	"github.com/fitan/fxkit/cli"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/consulx"
	"github.com/fitan/fxkit/docs"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/hatchetx"
	"github.com/fitan/fxkit/huma"
	"github.com/fitan/fxkit/mcpx"
	"github.com/fitan/fxkit/otelx"
	"github.com/fitan/fxkit/outbox"
	"github.com/fitan/fxkit/reqx"
	"github.com/fitan/fxkit/server"
	"github.com/fitan/fxkit/di"
)

func init() {
	// 示例用途：被 gitignore 的 example/tmp/hatchet.token（不要把 JWT 放进受版本控制的 yaml）。
	if strings.TrimSpace(os.Getenv("HATCHET_CLIENT_TOKEN")) != "" {
		return
	}
	for _, p := range []string{"tmp/hatchet.token", filepath.Join("example", "tmp", "hatchet.token")} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		tok := strings.TrimSpace(strings.ReplaceAll(string(b), "\r", ""))
		if tok == "" {
			continue
		}
		_ = os.Setenv("HATCHET_CLIENT_TOKEN", tok)
		return
	}
}

// WithMergedDocs 与 [github.com/fitan/fxkit.Default] 一致，区别在于 /docs 和
// /openapi.{yaml,json} 会把 baseYAML 与实时的 Huma spec 合并。
// 只想要 Huma 时传 nil/空 baseYAML 即可。
//
// 相比 Default()，这里额外注册了一个**启动自检**：在 PreStart 的最后阶段
// 校验关键 group 非空。如果某个业务模块忘记加进 Run(...)、或贡献点签名
// 不受支持，装配会「成功」但路由/huma operation 全丢 —— 自检把这种情况
// 变成启动期硬失败，而不是上线后才发现部分接口 404。
func WithMergedDocs(baseYAML []byte) di.Option {
	return di.Options(
		config.Module,
		buildinfo.Module,
		otelx.Module,
		server.Module,
		server.ListenerModule,
		docs.MergedModule(baseYAML),
		gormx.Module,
		hatchetx.Module,
		outbox.Module,
		authz.Module,
		consulx.Module,
		reqx.Module,
		mcpx.Module,
		cli.Module,
		assertWiring(),
	)
}

// assertWiring 在 PreStart 结束前校验关键贡献点非空。
//
// 用 PhaseMountHTTP+1：此时 huma registrar 已安装、路由已挂载，
// 所有延迟构造函数都已求值，group 内容是最完整的。
//
// 只断言**必然存在**的 group。HealthChecker 不在此列 ——
// 未配置数据库/缓存时没有任何检查项是合法状态。
func assertWiring() di.Option {
	return func(i di.Injector) {
		lc, err := di.Invoke[*di.Lifecycle](i)
		if err != nil || lc == nil {
			return
		}
		lc.AppendPreStartPhase(di.PhaseMountHTTP+1, di.Hook{
			OnStart: func(context.Context) error {
				return di.AssertGroupsNonEmpty(i,
					di.GroupOf[huma.Registrar](),
					di.GroupOf[server.Route](),
				)
			},
		})
	}
}
