package buildinfo

import (
	"context"
	"log/slog"

	"go.uber.org/fx"
)

func registerStartupLog(lc fx.Lifecycle) {
	lc.Append(fx.Hook{
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
