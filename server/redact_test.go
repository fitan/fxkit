package server

import (
	"strings"
	"testing"
)

// TestRedactPreview_completeJSON 结构化路径：按键替换，保留其它字段。
func TestRedactPreview_completeJSON(t *testing.T) {
	got := redactPreview(`{"password":"p@ss","name":"alice"}`)
	if strings.Contains(got, "p@ss") {
		t.Fatalf("password leaked: %s", got)
	}
	if !strings.Contains(got, `"alice"`) {
		t.Fatalf("non-sensitive field dropped: %s", got)
	}
}

// TestRedactPreview_truncatedJSONStillRedactsKeys 是回归测试：
//
// 请求/响应体先按 maxLoggedBytes 截断、再交给 redactPreview，所以大 JSON 必然
// Unmarshal 失败。早期实现在这条路径上「命中敏感键就整段丢弃」，
// 结果是超过 8KB 的响应在日志里完全不可观测（正是注释声称修过的行为）。
func TestRedactPreview_truncatedJSONStillRedactsKeys(t *testing.T) {
	big := `{"token":"s3cr3t-value","data":[` +
		strings.Repeat(`{"k":"v"},`, 2000) + `{"k":"v"}]}`
	const loggedBytes = 8 * 1024 // 与 requestBodyLogMiddleware 内的 maxLoggedBytes 一致
	if len(big) <= loggedBytes {
		t.Fatalf("test payload must exceed %d bytes, got %d", loggedBytes, len(big))
	}
	truncated := big[:loggedBytes]

	got := redactPreview(truncated)
	if strings.Contains(got, "s3cr3t-value") {
		t.Fatalf("secret leaked from truncated body: %s", got)
	}
	if !strings.Contains(got, `"k"`) {
		t.Fatalf("non-sensitive content should stay observable, got: %.80s", got)
	}
}

// TestRedactPreview_unterminatedValue 值被截断在中间时也要掩掉，而不是原样输出。
func TestRedactPreview_unterminatedValue(t *testing.T) {
	got := redactPreview(`{"token":"abc`)
	if strings.Contains(got, "abc") {
		t.Fatalf("truncated secret leaked: %s", got)
	}
}
