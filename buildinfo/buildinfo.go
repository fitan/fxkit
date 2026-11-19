// Package buildinfo 暴露链接时注入的构建元数据。
//
// 在 Makefile 中可 include [ldflags.mk] 或手动设置 GO_LDFLAGS：
//
//	include path/to/fxkit/buildinfo/ldflags.mk
//	go build -ldflags "$(GO_LDFLAGS)" -o bin/server ./cmd
package buildinfo

import (
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/fitan/fxkit/di"
)

// mus 保护下面这些由 -ldflags 注入的包级变量。
//
// 它们只在构建时写入，但 Normalize 会把规范化后的值回写。
// 读路径不直接加锁：Get/Meta 读的是 [snapshot]（原子指针），
// 避免 /version 每个请求都抢全局写锁并重新 normalize。
var mus sync.RWMutex

// snapshot 是规范化后的不可变快照，由 Normalize / 首次读取填充。
var snapshot atomic.Pointer[Info]

// 以下变量在构建时通过 -ldflags 注入。
var (
	// Version 是版本号，默认 "dev"；未注入时回退为 12 位 commit，工作区有改动时追加 "-dirty"。
	Version = "dev"
	// Commit 是提交哈希，默认 "unknown"；未注入时可由 vcs.revision 回填（截断为 12 位）。
	Commit = "unknown"
	// GitRemote 是仓库远端地址，默认 "unknown"；不从构建信息回填。
	GitRemote = "unknown"
	// GitBranch 是分支名，默认 "unknown"；不从构建信息回填。
	GitBranch = "unknown"
	// Dirty 是工作区是否有未提交改动的字符串，默认 "false"；解析时接受 true/1。
	Dirty = "false"
	// GoVersion 是构建所用 Go 版本，默认 "unknown"；未注入时回退 runtime 构建信息。
	GoVersion = "unknown"
	// BuildTime 是构建时间字符串，默认空；未注入时可由 vcs.time 回填。
	BuildTime = ""
	// BuiltBy 是构建者标识，默认空；不从构建信息回填。
	BuiltBy = ""
)

// Info 是构建元数据的不可变快照。
type Info struct {
	// Version 版本号，默认 "dev"；未注入 -ldflags 时回退为 12 位 commit，工作区有改动时追加 "-dirty"。
	Version string `json:"version"`
	// Commit 提交哈希，默认 "unknown"；可从 go build 的 vcs.revision 回填（截断为 12 位）。
	Commit string `json:"commit"`
	// GitRemote 仓库远端地址，默认 "unknown"。
	GitRemote string `json:"git_remote"`
	// GitBranch 分支名，默认 "unknown"。
	GitBranch string `json:"git_branch"`
	// Dirty 工作区是否有未提交改动，由字符串 "true"/"1" 解析得到。
	Dirty bool `json:"dirty"`
	// GoVersion 构建所用 Go 版本，默认 "unknown"，可从 runtime 构建信息回填。
	GoVersion string `json:"go_version"`
	// BuildTime 构建时间字符串，默认空；未注入时可从 vcs.time 回填。
	BuildTime string `json:"build_time"`
	// BuiltBy 构建者标识，默认空。
	BuiltBy string `json:"built_by"`
}

// Normalize 修剪空白并为未设置字段填入合理默认值。未应用链接时 -ldflags（例如 Air 使用裸 go build）时，
// 会从 debug.BuildInfo 的 vcs 设置回填。可安全多次调用（并发安全）。
func Normalize() {
	mus.Lock()
	normalizeLocked()
	info := buildInfoLocked()
	mus.Unlock()
	snapshot.Store(&info)
}

// current 返回当前快照；首次调用（或 Normalize 被外部重置后）会就地构建。
func current() Info {
	if p := snapshot.Load(); p != nil {
		return *p
	}
	mus.Lock()
	defer mus.Unlock()
	normalizeLocked()
	info := buildInfoLocked()
	snapshot.Store(&info)
	return info
}

// buildInfoLocked 从包级变量构造快照，调用方必须持有 mus。
func buildInfoLocked() Info {
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

// normalizeLocked 在持有 mus 时先回填 VCS 元数据，再修剪空白并为空字段填默认值。
func normalizeLocked() {
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

// shortRevision 去掉首尾空白并截断到 12 位。
func shortRevision(rev string) string {
	rev = strings.TrimSpace(rev)
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

// parseDirty 解析构建注入的 GIT_DIRTY（git 只产出 true/false，兼容 1/0）。
func parseDirty(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true":
		return true
	default:
		return false
	}
}

// Meta 返回可写入 Consul 等目录的构建字段。空值省略。
func Meta() map[string]string {
	info := current()
	pairs := []struct{ k, v string }{
		{"version", info.Version},
		{"build_commit", info.Commit},
		{"build_time", info.BuildTime},
		{"build_built_by", info.BuiltBy},
		{"git_remote", info.GitRemote},
		{"git_branch", info.GitBranch},
		{"git_dirty", strconv.FormatBool(info.Dirty)},
		{"go_version", info.GoVersion},
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

// Get 返回当前（已 Normalize）构建信息的快照。并发安全。
func Get() Info {
	return current()
}

// Module 使 [Info] 可用于 di 注入，并在启动时记录元数据。
// GET /version 由 [github.com/fitan/fxkit/server.RegisterHealth] 挂载。
var Module = di.Module("fxkit/buildinfo",
	func(i di.Injector) {
		di.Provide(i, func(i di.Injector) (Info, error) {
			return Get(), nil
		})
		lc := di.MustInvoke[*di.Lifecycle](i)
		registerStartupLog(lc)
	},
)
