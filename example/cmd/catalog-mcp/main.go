package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fitan/fxkit/mcpx"
)

//go:embed openapi.yaml
var openapiSpec []byte

func main() {
	var (
		transport = flag.String("transport", "stdio", "transport mode: stdio or http")
		port      = flag.Int("port", 9090, "port for http transport")
		baseURL   = flag.String("base-url", "http://127.0.0.1:8080", "target backend microservice URL")
	)
	flag.Parse()

	if envURL := os.Getenv("SERVICE_BASE_URL"); envURL != "" {
		*baseURL = envURL
	}

	tools, err := mcpx.ParseOpenAPI(openapiSpec)
	if err != nil {
		log.Fatalf("failed to parse openapi spec: %v", err)
	}

	srv := mcpx.NewServer(mcpx.ServerConfig{
		Name:    "catalog-mcp",
		Version: "1.0.0",
		BaseURL: *baseURL,
	}, tools)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch *transport {
	case "stdio":
		if err := srv.ServeStdio(ctx, os.Stdin, os.Stdout); err != nil {
			log.Fatalf("stdio server error: %v", err)
		}
	case "http":
		addr := fmt.Sprintf(":%d", *port)
		httpServer := &http.Server{
			Addr:    addr,
			Handler: srv.HTTPHandler(),
		}
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = httpServer.Shutdown(shutdownCtx)
		}()
		log.Printf("MCP HTTP server listening on %s (target backend: %s)", addr, *baseURL)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server error: %v", err)
		}
	default:
		log.Fatalf("unknown transport: %s (must be stdio or http)", *transport)
	}
}
