package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/server"
)

// maxResponseBytes 是可缓存响应体的上限（1 MiB）；超过后不再缓存、直接透写。
const maxResponseBytes = 1 << 20

// Middleware 对带 Idempotency-Key 的 mutating 请求做去重与响应重放。
func Middleware(cfg Config, store Store) server.Middleware {
	if !cfg.Enabled || store == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	allowed := make(map[string]bool, len(cfg.Methods))
	for _, m := range cfg.Methods {
		allowed[strings.ToUpper(strings.TrimSpace(m))] = true
	}
	header := strings.TrimSpace(cfg.Header)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !allowed[r.Method] {
				next.ServeHTTP(w, r)
				return
			}
			key := strings.TrimSpace(r.Header.Get(header))
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}

			body, err := io.ReadAll(io.LimitReader(r.Body, cfg.MaxBodyBytes+1))
			if err != nil {
				fxerrors.WriteError(w, r, fxerrors.BadRequest("read request body"))
				return
			}
			if int64(len(body)) > cfg.MaxBodyBytes {
				fxerrors.WriteError(w, r, fxerrors.BadRequest("request body too large for idempotency"))
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))

			sum := sha256.Sum256(append([]byte(r.Method+"\n"+r.URL.RequestURI()+"\n"), body...))
			hash := hex.EncodeToString(sum[:])

			scoped := scopedKey(r, key)
			beginAt := time.Now()
			rec, created, err := store.Begin(r.Context(), scoped, hash, cfg.TTL)
			recordStoreOp(r.Context(), "begin", time.Since(beginAt), err)
			if err != nil {
				recordRequest(r.Context(), "error")
				fxerrors.WriteError(w, r, fxerrors.Wrap(err))
				return
			}
			if !created {
				if !rec.Completed {
					recordRequest(r.Context(), "conflict")
					fxerrors.WriteError(w, r, fxerrors.Conflict("idempotency key is in progress"))
					return
				}
				if rec.RequestHash != hash {
					recordRequest(r.Context(), "conflict")
					fxerrors.WriteError(w, r, fxerrors.Conflict("idempotency key reused with a different request"))
					return
				}
				recordRequest(r.Context(), "replayed")
				if rec.ContentType != "" {
					w.Header().Set("Content-Type", rec.ContentType)
				}
				w.Header().Set("Idempotent-Replayed", "true")
				w.WriteHeader(rec.StatusCode)
				_, _ = w.Write(rec.ResponseBody)
				return
			}
			recordRequest(r.Context(), "first")

			cw := &captureWriter{ResponseWriter: w, header: make(http.Header)}
			defer func() {
				if rec := recover(); rec != nil {
					_ = store.Abort(context.WithoutCancel(r.Context()), scoped)
					panic(rec)
				}
			}()
			next.ServeHTTP(cw, r)
			status := cw.statusCode()
			contentType := cw.header.Get("Content-Type")
			respBody := append([]byte(nil), cw.body...)
			streaming, overflow := cw.streaming, cw.overflow
			cw.flush()

			writeCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
			defer cancel()
			if status >= 200 && status < 500 && !streaming && !overflow {
				completeAt := time.Now()
				err := store.Complete(writeCtx, scoped, status, contentType, respBody)
				recordStoreOp(r.Context(), "complete", time.Since(completeAt), err)
				if err != nil {
					slog.WarnContext(writeCtx, "idempotency complete failed", "error", err)
				}
			} else {
				abortAt := time.Now()
				err := store.Abort(writeCtx, scoped)
				recordStoreOp(r.Context(), "abort", time.Since(abortAt), err)
				if err != nil {
					slog.WarnContext(writeCtx, "idempotency abort failed", "error", err)
				}
			}
		})
	}
}

// scopedKey 把客户端 key 限定在「方法 + 路径 + 主体」作用域内，
// 避免不同接口/不同用户复用同一个 Idempotency-Key 时互相重放或误报 409。
// 不包含 query：query 变化由 request hash 区分；包含 path 足以隔离资源。
func scopedKey(r *http.Request, clientKey string) string {
	subject := ""
	if subj, ok := authz.SubjectFromContext(r.Context()); ok {
		subject = subj.ID
	}
	return ScopeKey(r.Method, r.URL.Path, subject, clientKey)
}

// ScopeKey 返回中间件实际使用的存储键（method + path + subject + client key 的 sha256）。
// 正常请求不需要调用；用于测试、排查和运维清理。
func ScopeKey(method, path, subject, clientKey string) string {
	raw := method + "\n" + path + "\n" + subject + "\n" + clientKey
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// captureWriter 缓冲 handler 的响应，供中间件在请求结束后写入幂等存储。
type captureWriter struct {
	http.ResponseWriter
	// header 是待写出的响应头，flush 时复制到真实 ResponseWriter。
	header http.Header
	// status 是记录的首个状态码，0 表示尚未 WriteHeader。
	status int
	// body 是已缓冲的响应体，flush 后清空。
	body []byte
	// streaming 表示 handler 调用过 Flush（如 SSE），此类响应不缓存。
	streaming bool
	// overflow 表示已停止缓冲、直接透写（缓冲超限或 handler 调用 Flush）。
	overflow bool
	// wroteHeader 表示响应头已写给真实 ResponseWriter（只写一次）。
	wroteHeader bool
}

// Header 返回待写出的响应头集合。
func (w *captureWriter) Header() http.Header { return w.header }

// WriteHeader 只记录首个状态码，不立即写出。
func (w *captureWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

// Write 缓冲响应体；超过 maxResponseBytes 后转为 overflow 并直接透写。
func (w *captureWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.overflow {
		return w.ResponseWriter.Write(b)
	}
	if len(w.body)+len(b) > maxResponseBytes {
		w.overflow = true
		w.flush()
		return w.ResponseWriter.Write(b)
	}
	w.body = append(w.body, b...)
	return len(b), nil
}

// Flush 标记发生了流式写出，并立即把已缓冲内容刷给真实 ResponseWriter。
func (w *captureWriter) Flush() {
	w.streaming = true
	w.overflow = true
	w.flush()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap 返回被包装的 ResponseWriter，供 http.ResponseController 使用。
func (w *captureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// statusCode 返回已记录的状态码，未写时为 200。
func (w *captureWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// flush 把已缓冲的响应头与响应体写出到真实 ResponseWriter（只写一次）。
func (w *captureWriter) flush() {
	if !w.wroteHeader {
		dst := w.ResponseWriter.Header()
		for k, v := range w.header {
			dst[k] = append([]string(nil), v...)
		}
		w.ResponseWriter.WriteHeader(w.statusCode())
		w.wroteHeader = true
	}
	if len(w.body) > 0 {
		_, _ = w.ResponseWriter.Write(w.body)
		w.body = nil
	}
}
