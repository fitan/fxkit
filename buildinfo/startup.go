package buildinfo

import (
	"context"
	"log/slog"

	"github.com/fitan/fxkit/di"
)

// registerStartupLog 在 lc 非空时注册 OnStart 钩子，启动时以 Info 级别打印构建信息。
func registerStartupLog(lc *di.Lifecycle) {
	if lc == nil {
		return
	}
	lc.Append(di.Hook{
		OnStart: func(context.Context) error {
			info := Get()
			slog.Info("build info",
				"version", info.Version,
				"commit", info.Commit,
				"git_remote", info.GitRemote,
				"git_branch", info.GitBranch,
				"dirty", info.Dirty,
				"go_version", info.GoVersion,
				"build_time", info.BuildTime,
				"built_by", info.BuiltBy,
			)
			return nil
		},
	})
}
