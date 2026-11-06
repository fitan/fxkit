package server

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
)

// TestRegisterRoutes_RejectsInvalidEntries 是回归测试：
// 非法 Route（空 pattern / nil handler / 未知 method）必须让启动失败，
// 并且一次报出全部问题条目。早期实现是「跳过 + 告警」，结果是
// 服务正常启动、依赖它的调用方收到 404；而 chi 自己只会 panic 出
// "routing pattern must begin with '/' in ”"，无法定位来源模块。
func TestRegisterRoutes_RejectsInvalidEntries(t *testing.T) {
	mux := chi.NewRouter()

	err := RegisterRoutes(RouterParams{
		Mux: mux,
		Routes: []Route{
			{Pattern: "", Handler: http.NotFoundHandler()},         // 空 pattern
			{Pattern: "no-slash", Handler: http.NotFoundHandler()}, // 缺前导斜杠
			{Pattern: "/nil-handler", Handler: nil},                // 处理器为 nil
			{Pattern: "/bad-method", Handler: http.NotFoundHandler(), Methods: []string{""}},
			{Pattern: "/typo-method", Handler: http.NotFoundHandler(), Methods: []string{"GTE"}},
			{Pattern: "/ok", Handler: http.NotFoundHandler()}, // 唯一合法的
		},
	})
	if err == nil {
		t.Fatal("invalid routes must fail registration, got nil error")
	}

	// 错误必须包含全部 5 条问题路由，方便一次改完。
	for _, want := range []string{"route[0]", "route[1]", "route[2]", "route[3]", "route[4]", "GTE"} {
		assert.Contains(t, err.Error(), want)
	}

	// 校验先于挂载：失败时不应该留下「挂了一半」的 mux。
	rec := doGet(t, mux, "/ok")
	assert.Equal(t, http.StatusNotFound, rec.Code, "nothing should be mounted when validation fails")
}

// TestValidateRoute 单测校验规则。
func TestValidateRoute(t *testing.T) {
	cases := []struct {
		name    string
		route   Route
		wantErr bool
	}{
		{"valid", Route{Pattern: "/a", Handler: http.NotFoundHandler()}, false},
		{"valid with methods", Route{Pattern: "/a", Handler: http.NotFoundHandler(), Methods: []string{"GET"}}, false},
		{"empty pattern", Route{Pattern: "  ", Handler: http.NotFoundHandler()}, true},
		{"missing slash", Route{Pattern: "a", Handler: http.NotFoundHandler()}, true},
		{"nil handler", Route{Pattern: "/a"}, true},
		{"empty method", Route{Pattern: "/a", Handler: http.NotFoundHandler(), Methods: []string{" "}}, true},
		{"unsupported method", Route{Pattern: "/a", Handler: http.NotFoundHandler(), Methods: []string{"GTE"}}, true},
		{"lowercase method ok", Route{Pattern: "/a", Handler: http.NotFoundHandler(), Methods: []string{"get"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRoute(tc.route)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
