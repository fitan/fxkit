package consulx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// statusError 表示 Consul 返回了非 2xx 状态码。
// 用于区分 4xx（致命，重试无意义）与 5xx/429（瞬时可重试）。
type statusError struct {
	// Op 操作名，作为错误信息前缀（如 "consul acquire kv"）。
	Op string
	// Code Consul 返回的 HTTP 状态码，isRetryable 据此区分致命与可重试错误。
	Code int
	// Body 响应体（已去除首尾空白），可能为空。
	Body string
}

// Error 返回 "op: status=code" 形式的描述，有响应体时附加 ": body"。
func (e *statusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("%s: status=%d", e.Op, e.Code)
	}
	return fmt.Sprintf("%s: status=%d: %s", e.Op, e.Code, e.Body)
}

// readBodyAndCheck 读取响应体，并在非 2xx 时返回 *statusError。
func readBodyAndCheck(op string, resp *http.Response) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return body, fmt.Errorf("%s: read body: %w", op, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, &statusError{Op: op, Code: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	return body, nil
}

// isStatusError 判断 err 是否为指定状态码的 Consul 响应。
func isStatusError(err error, code int) bool {
	var se *statusError
	return errors.As(err, &se) && se.Code == code
}

// isRetryable 判断错误是否值得在 ctx 内退避重试：
//   - 5xx / 429 / 408（服务端瞬时故障）
//   - 网络传输错误（timeout、连接中断等 net.Error）
//
// 4xx（除 408/429）与本地解析错误视为致命，立即失败。
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	var se *statusError
	if errors.As(err, &se) {
		return se.Code >= 500 ||
			se.Code == http.StatusTooManyRequests ||
			se.Code == http.StatusRequestTimeout
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// sleepCtx 睡眠 d，若 ctx 先结束则返回 false。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// cleanupContext 返回用于后台清理（release/destroy）的独立超时 context，
// 避免调用方 ctx 已取消导致锁无法释放。
func cleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// backoffNext 返回下一次退避时长（上限 5s）。
func backoffNext(d time.Duration) time.Duration {
	if d <= 0 {
		return 250 * time.Millisecond
	}
	d *= 2
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}
