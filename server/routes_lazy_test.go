package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/server"
)

func TestProvideRoutesLazyRegistersGroup(t *testing.T) {
	inj := di.New()
	server.ProvideRoutesLazy(func(di.Injector) ([]server.Route, error) {
		return []server.Route{{
			Pattern: "/x",
			Methods: []string{http.MethodGet},
			Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		}}, nil
	})(inj)
	if err := di.MustInvoke[*di.Lifecycle](inj).StartPreStartOnly(context.Background()); err != nil {
		t.Fatal(err)
	}
	routes := di.GetGroup[server.Route](inj)
	if len(routes) != 1 || routes[0].Pattern != "/x" {
		t.Fatalf("routes=%+v", routes)
	}
}
