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
			rec, created, err := store.Begin(r.Context(), scoped, hash, cfg.TTL)
			if err != nil {
				fxerrors.WriteError(w, r, fxerrors.Wrap(err))
				return
			}
			if !created {
				if !rec.Completed {
					fxerrors.WriteError(w, r, fxerrors.Conflict("idempotency key is in progress"))
					return
				}
				if rec.RequestHash != hash {
					fxerrors.WriteError(w, r, fxerrors.Conflict("idempotency key reused with a different request"))
					return
				}
				if rec.ContentType != "" {
					w.Header().Set("Content-Type", rec.ContentType)
				}
				w.Header().Set("Idempotent-Replayed", "true")
				w.WriteHeader(rec.StatusCode)
				_, _ = w.Write(rec.ResponseBody)
				return
			}

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
				if err := store.Complete(writeCtx, scoped, status, contentType, respBody); err != nil {
					slog.WarnContext(writeCtx, "idempotency complete failed", "error", err)
				}
			} else if err := store.Abort(writeCtx, scoped); err != nil {
				slog.WarnContext(writeCtx, "idempotency abort failed", "error", err)
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

type captureWriter struct {
	http.ResponseWriter
	header      http.Header
	status      int
	body        []byte
	streaming   bool
	overflow    bool
	wroteHeader bool
}

func (w *captureWriter) Header() http.Header { return w.header }

func (w *captureWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

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

func (w *captureWriter) Flush() {
	w.streaming = true
	w.overflow = true
	w.flush()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *captureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *captureWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

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
