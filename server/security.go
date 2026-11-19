package server

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/fitan/fxkit/fxerrors"
)

// securityHeadersMiddleware 设置安全响应头；HSTS 仅在 TLS 请求上添加，未启用时原样透传。
func securityHeadersMiddleware(cfg SecurityHeadersConfig) Middleware {
	if !cfg.Enabled {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			if cfg.HSTS && r.TLS != nil {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			if csp := strings.TrimSpace(cfg.CSP); csp != "" {
				h.Set("Content-Security-Policy", csp)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// csrfMiddleware 对非安全方法校验 cookie 与 header 中的 token（常量时间比较），失败返回 403；未启用时原样透传。
func csrfMiddleware(cfg CSRFConfig) Middleware {
	if !cfg.Enabled {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
				next.ServeHTTP(w, r)
				return
			}
			cookie, err := r.Cookie(cfg.CookieName)
			if err != nil || cookie.Value == "" {
				recordRejection(r.Context(), "csrf_cookie_missing")
				fxerrors.WriteError(w, r, fxerrors.PermissionDenied("csrf cookie missing"))
				return
			}
			token := r.Header.Get(cfg.HeaderName)
			if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(cookie.Value)) != 1 {
				recordRejection(r.Context(), "csrf_token_invalid")
				fxerrors.WriteError(w, r, fxerrors.PermissionDenied("csrf token invalid"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
