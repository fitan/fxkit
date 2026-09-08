// Package stack is fxkit.Default() with docs.MergedModule instead of the
// placeholder Scalar skeleton. Example services need live Huma on /docs.
package stack

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/buildinfo"
	"github.com/fitan/fxkit/cli"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/consulx"
	"github.com/fitan/fxkit/docs"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/hatchetx"
	"github.com/fitan/fxkit/otelx"
	"github.com/fitan/fxkit/outbox"
	"github.com/fitan/fxkit/reqx"
	"github.com/fitan/fxkit/server"
	"go.uber.org/fx"
)

func init() {
	// Showcase: gitignored example/tmp/hatchet.token (do not put JWT in tracked yaml).
	if strings.TrimSpace(os.Getenv("HATCHET_CLIENT_TOKEN")) != "" {
		return
	}
	for _, p := range []string{"tmp/hatchet.token", filepath.Join("example", "tmp", "hatchet.token")} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		tok := strings.TrimSpace(strings.ReplaceAll(string(b), "\r", ""))
		if tok == "" {
			continue
		}
		_ = os.Setenv("HATCHET_CLIENT_TOKEN", tok)
		return
	}
}

// WithMergedDocs matches [github.com/fitan/fxkit.Default] except /docs and
// /openapi.{yaml,json} merge baseYAML with the live Huma spec.
// Pass nil/empty baseYAML for Huma-only.
func WithMergedDocs(baseYAML []byte) fx.Option {
	return fx.Options(
		config.Module,
		buildinfo.Module,
		otelx.Module,
		server.Module,
		server.ListenerModule,
		docs.MergedModule(baseYAML),
		gormx.Module,
		hatchetx.Module,
		outbox.Module,
		authz.Module,
		consulx.Module,
		reqx.Module,
		cli.Module,
	)
}
