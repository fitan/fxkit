package server

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
)

// TestRegisterRoutes_SkipsInvalidEntries 是回归测试：
// 非法 Route（空 pattern / nil handler）过去会让 chi 在 PreStart 里 panic，
// 堆栈只剩 "routing pattern must begin with '/' in ”"，无法定位来源模块。
// 现在跳过并记录带 pattern 的告警。
func TestRegisterRoutes_SkipsInvalidEntries(t *testing.T) {
	mux := chi.NewRouter()

	assert.NotPanics(t, func() {
		RegisterRoutes(RouterParams{
			Mux: mux,
			Routes: []Route{
				{Pattern: "", Handler: http.NotFoundHandler()},         // 空 pattern
				{Pattern: "no-slash", Handler: http.NotFoundHandler()}, // 缺前导斜杠
				{Pattern: "/nil-handler", Handler: nil},                // nil handler
				{Pattern: "/bad-method", Handler: http.NotFoundHandler(), Methods: []string{""}},
				{Pattern: "/ok", Handler: http.NotFoundHandler()}, // 唯一合法的
			},
		})
	}, "invalid routes must be skipped, not panic")

	// 只有合法路由被挂载
	rec := doGet(t, mux, "/ok")
	assert.Equal(t, http.StatusNotFound, rec.Code, "/ok is mounted (returns handler's 404)")
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
