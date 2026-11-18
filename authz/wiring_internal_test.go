package authz

import (
	"testing"

	"github.com/fitan/fxkit/di"
)

// TestProvideMuxMiddleware_FailsLoudWhenConfigMissing：鉴权接线失败必须 panic，
// 不能降级成「中间件没装上、服务照常监听」。
func TestProvideMuxMiddleware_FailsLoudWhenConfigMissing(t *testing.T) {
	root := di.New()

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when *Config cannot be resolved; got a silent skip (fail-open)")
		}
	}()

	_, _ = provideMuxMiddleware(root)
}
