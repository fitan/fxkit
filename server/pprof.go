package server

import (
	"net/http"
	"net/http/pprof"
	"strings"

	"github.com/go-chi/chi/v5"
)

// mountPprof 把 net/http/pprof 挂到 chi mux。
//
// 默认关闭；开启后仍会经过 chi 上的 authz / 限流等中间件。
func mountPprof(r chi.Router, prefix string) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "/debug/pprof/"
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	r.Get(prefix, pprof.Index)
	r.Get(prefix+"cmdline", pprof.Cmdline)
	r.Get(prefix+"profile", pprof.Profile)
	r.Get(prefix+"symbol", pprof.Symbol)
	r.Post(prefix+"symbol", pprof.Symbol)
	r.Get(prefix+"trace", pprof.Trace)
	r.Get(prefix+"{profile}", func(w http.ResponseWriter, r *http.Request) {
		pprof.Handler(chi.URLParam(r, "profile")).ServeHTTP(w, r)
	})
}
