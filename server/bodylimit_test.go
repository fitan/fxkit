package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// echoBodyLen 读完整 body，返回读到的字节数与是否超限。
func echoBodyLen(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			if IsBodyTooLarge(err) {
				http.Error(w, "too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, strings.Repeat("x", 0))
		w.WriteHeader(http.StatusOK)
		w.Header().Set("X-Bytes", strings.TrimSpace(itoa64(n)))
	})
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// TestRegisterRoutes_DefaultBodyLimit 是回归测试：
// 裸 chi 路由过去**没有**请求体上限（只有 Huma 路由有 1 MiB 默认值），
// 客户端可以往任意 POST 端点灌超大 body。现在默认限制 1 MiB。
func TestRegisterRoutes_DefaultBodyLimit(t *testing.T) {
	mux := chi.NewRouter()
	require.NoError(t, RegisterRoutes(RouterParams{
		Mux:          mux,
		MaxBodyBytes: 1 << 20,
		Routes: []Route{{
			Pattern: "/upload",
			Methods: []string{http.MethodPost},
			Handler: echoBodyLen(t),
		}},
	}))

	// 超限：应被 MaxBytesReader 截断并由 handler 转成 413。
	big := bytes.Repeat([]byte("a"), (1<<20)+1)
	req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(big))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)

	// 未超限：正常通过。
	small := bytes.Repeat([]byte("a"), 1024)
	req = httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(small))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestRegisterRoutes_PerRouteOverride 验证单路由覆盖：
// 负数表示不限制（文件上传），正数覆盖默认值。
func TestRegisterRoutes_PerRouteOverride(t *testing.T) {
	mux := chi.NewRouter()
	require.NoError(t, RegisterRoutes(RouterParams{
		Mux:          mux,
		MaxBodyBytes: 1024,
		Routes: []Route{
			{Pattern: "/unlimited", Methods: []string{http.MethodPost}, Handler: echoBodyLen(t), MaxBodyBytes: -1},
			{Pattern: "/strict", Methods: []string{http.MethodPost}, Handler: echoBodyLen(t), MaxBodyBytes: 16},
		},
	}))

	payload := bytes.Repeat([]byte("a"), 4096)

	// 不限制：4KB 也能过（默认值 1KB 被覆盖）。
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/unlimited", bytes.NewReader(payload)))
	assert.Equal(t, http.StatusOK, rec.Code)

	// 更严格：16B 上限，4KB 应 413。
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/strict", bytes.NewReader(payload)))
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

// TestConfig_DefaultMaxBodyBytes 默认值必须是 1 MiB，且可被 yaml 覆盖。
func TestConfig_DefaultMaxBodyBytes(t *testing.T) {
	var c Config
	c.SetDefaults()
	assert.EqualValues(t, 1<<20, c.MaxBodyBytes)
}
