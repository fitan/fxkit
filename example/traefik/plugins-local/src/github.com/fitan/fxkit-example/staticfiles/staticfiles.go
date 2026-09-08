package staticfiles

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"
)

type Config struct {
	Root string `json:"root,omitempty"`
}

func CreateConfig() *Config {
	return &Config{Root: "/web"}
}

func New(_ context.Context, next http.Handler, cfg *Config, _ string) (http.Handler, error) {
	root := "/web"
	if cfg != nil && strings.TrimSpace(cfg.Root) != "" {
		root = strings.TrimSpace(cfg.Root)
	}
	st, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("staticfiles: root %q: %w", root, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("staticfiles: root %q is not a directory", root)
	}
	fs := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		// SPA fallback so Logto can redirect to /callback.
		// Do not rewrite to /index.html: FileServer 301s that to / and drops ?code=.
		if r.URL.Path == "/callback" || r.URL.Path == "/callback/" {
			r.URL.Path = "/"
		}
		if ct := typeByPath(r.URL.Path); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		fs.ServeHTTP(w, r)
	}), nil
}

func typeByPath(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	default:
		if p == "/" || p == "" {
			return "text/html; charset=utf-8"
		}
		return ""
	}
}
