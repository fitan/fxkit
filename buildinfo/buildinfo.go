// Package buildinfo 暴露链接时注入的构建元数据。
//
// 在 Makefile 中可 include [ldflags.mk] 或手动设置 GO_LDFLAGS：
//
//	include path/to/fxkit/buildinfo/ldflags.mk
//	go build -ldflags "$(GO_LDFLAGS)" -o bin/server ./cmd
package buildinfo

import (
	"runtime/debug"
	"strings"

	"go.uber.org/fx"
)

// 以下变量在构建时通过 -ldflags 注入。
var (
	Version   = "dev"
	Commit    = "unknown"
	GitRemote = "unknown"
	GitBranch = "unknown"
	Dirty     = "false"
	GoVersion = "unknown"
	BuildTime = ""
	BuiltBy   = ""
)

// Info 是构建元数据的不可变快照。
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	GitRemote string `json:"git_remote"`
	GitBranch string `json:"git_branch"`
	Dirty     bool   `json:"dirty"`
	GoVersion string `json:"go_version"`
	BuildTime string `json:"build_time"`
	BuiltBy   string `json:"built_by"`
}

// Normalize 修剪空白并为未设置字段填入合理默认值。未应用链接时 -ldflags（例如 Air 使用裸 go build）时，
// 会从 debug.BuildInfo 的 vcs 设置回填。可安全多次调用。
func Normalize() {
	applyVCSFallback()
	Version = strings.TrimSpace(Version)
	Commit = strings.TrimSpace(Commit)
	GitRemote = strings.TrimSpace(GitRemote)
	GitBranch = strings.TrimSpace(GitBranch)
	Dirty = strings.TrimSpace(Dirty)
	GoVersion = strings.TrimSpace(GoVersion)
	BuildTime = strings.TrimSpace(BuildTime)
	BuiltBy = strings.TrimSpace(BuiltBy)
	if Version == "" {
		Version = "dev"
	}
	if Commit == "" {
		Commit = "unknown"
	}
	if GitRemote == "" {
		GitRemote = "unknown"
	}
	if GitBranch == "" {
		GitBranch = "unknown"
	}
	if GoVersion == "" {
		GoVersion = "unknown"
	}
}

// applyVCSFallback 在未通过 -ldflags 设置时（Air 未设置 GO_LDFLAGS 时常见），
// 将 go build -buildvcs 元数据复制到包变量。
func applyVCSFallback() {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if GoVersion == "" || GoVersion == "unknown" {
		if bi.GoVersion != "" {
			GoVersion = bi.GoVersion
		}
	}
	var vcsRev, vcsTime, vcsModified string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			vcsRev = s.Value
		case "vcs.time":
			vcsTime = s.Value
		case "vcs.modified":
			vcsModified = s.Value
		}
	}
	if vcsRev == "" {
		return
	}
	if Commit == "" || Commit == "unknown" {
		Commit = shortRevision(vcsRev)
	}
	if Version == "" || Version == "dev" {
		Version = shortRevision(vcsRev)
		if vcsModified == "true" {
			Version += "-dirty"
		}
	}
	if BuildTime == "" && vcsTime != "" {
		BuildTime = vcsTime
	}
	if Dirty == "" || Dirty == "false" {
		if vcsModified == "true" {
			Dirty = "true"
		}
	}
}

func shortRevision(rev string) string {
	rev = strings.TrimSpace(rev)
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

func parseDirty(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "y", "dirty":
		return true
	default:
		return false
	}
}

// Meta 返回可写入 Consul 等目录的构建字段。空值省略。
func Meta() map[string]string {
	Normalize()
	pairs := []struct{ k, v string }{
		{"version", Version},
		{"build_commit", Commit},
		{"build_time", BuildTime},
		{"build_built_by", BuiltBy},
		{"git_remote", GitRemote},
		{"git_branch", GitBranch},
		{"git_dirty", Dirty},
		{"go_version", GoVersion},
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		if strings.TrimSpace(p.v) == "" {
			continue
		}
		out[p.k] = p.v
	}
	return out
}

// Get 返回当前（已 Normalize）构建信息的快照。
func Get() Info {
	Normalize()
	return Info{
		Version:   Version,
		Commit:    Commit,
		GitRemote: GitRemote,
		GitBranch: GitBranch,
		Dirty:     parseDirty(Dirty),
		GoVersion: GoVersion,
		BuildTime: BuildTime,
		BuiltBy:   BuiltBy,
	}
}

// Module 使 [Info] 可用于 Fx 注入，并在启动时记录元数据。
// GET /version 由 [github.com/fitan/fxkit/server.RegisterHealth] 挂载。
var Module = fx.Module("fxkit/buildinfo",
	fx.Provide(Get),
	fx.Invoke(registerStartupLog),
)
