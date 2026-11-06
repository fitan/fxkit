package authz

import (
	"testing"

	"github.com/fitan/fxkit/di"
)

// TestProvideHumaMiddleware_FailsLoudWhenConfigMissing 是回归测试：
//
// 早期实现在解析 *Config 失败时 `return nil`，而 huma 模块对 nil 中间件
// 直接跳过安装（`if m := mw.Middleware(api); m != nil`）。结果是服务正常启动、
// 端口正常监听、所有请求不再鉴权，只留下一条 ERROR 日志。
//
// 现在这里必须 panic —— 鉴权是安全边界，接线错误不能降级成「不鉴权」。
func TestProvideHumaMiddleware_FailsLoudWhenConfigMissing(t *testing.T) {
	root := di.New() // 容器里没有 authz 的 *Config
	reg, _ := provideHumaMiddleware(root)

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when *Config cannot be resolved; got a silent skip (fail-open)")
		}
	}()

	_ = reg.Middleware(nil)
}

// TestProvideMuxMiddleware_FailsLoudWhenConfigMissing 同上，覆盖 mux 模式。
func TestProvideMuxMiddleware_FailsLoudWhenConfigMissing(t *testing.T) {
	root := di.New()

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when *Config cannot be resolved; got a silent skip (fail-open)")
		}
	}()

	_, _ = provideMuxMiddleware(root)
}
