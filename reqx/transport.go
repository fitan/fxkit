package reqx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
)

const (
	defaultMaxFailover       = 5
	defaultMaxRewindBodySize = 4 * 1024 * 1024 // 4MB ceiling for buffering request body
)

// resolveTransport picks a discovered endpoint and rewrites the request host,
// then delegates to the next RoundTripper (typically otelhttp → base transport).
// On transport / selected 5xx failures it tries another endpoint, for any method.
type resolveTransport struct {
	pool        *endpointPool
	scheme      string
	name        string
	maxFailover int
	next        http.RoundTripper
}

func (t *resolveTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := ensureRewindableBody(req); err != nil {
		return nil, fmt.Errorf("reqx(%s): rewind body: %w", t.name, err)
	}
	n := t.pool.len()
	if n == 0 {
		return nil, fmt.Errorf("reqx(%s): no healthy endpoints", t.name)
	}

	attempts := n
	if t.maxFailover > 0 && t.maxFailover < attempts {
		attempts = t.maxFailover
	}
	if attempts > defaultMaxFailover {
		attempts = defaultMaxFailover
	}

	scheme := t.scheme
	if scheme == "" {
		scheme = "http"
	}

	var lastErr error
	tried := make(map[string]struct{}, attempts)
	hasUnrewindableBody := req.Body != nil && req.Body != http.NoBody && req.GetBody == nil

	for i := 0; i < attempts; i++ {
		ep, err := t.pool.next()
		if err != nil {
			return nil, fmt.Errorf("reqx(%s): %w", t.name, err)
		}
		if _, ok := tried[ep]; ok {
			// Already tried this host; keep rotating until fresh or exhausted.
			if len(tried) >= n {
				break
			}
			i--
			continue
		}
		tried[ep] = struct{}{}

		r2, err := cloneForEndpoint(req, ep, scheme)
		if err != nil {
			return nil, fmt.Errorf("reqx(%s): %w", t.name, err)
		}

		resp, err := t.next.RoundTrip(r2)
		if err == nil {
			if shouldFailoverStatus(resp.StatusCode) && i+1 < attempts && len(tried) < n {
				if hasUnrewindableBody {
					// Cannot rewind large or streaming body safely; do not retry with empty body
					return resp, nil
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				lastErr = fmt.Errorf("reqx(%s): endpoint %s status %d", t.name, ep, resp.StatusCode)
				continue
			}
			return resp, nil
		}

		lastErr = err
		if !isFailoverError(err) || i+1 >= attempts || hasUnrewindableBody {
			return nil, fmt.Errorf("reqx(%s): endpoint %s: %w", t.name, ep, err)
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("reqx(%s): no healthy endpoints", t.name)
	}
	return nil, lastErr
}

func cloneForEndpoint(req *http.Request, ep, scheme string) (*http.Request, error) {
	if req.URL == nil {
		return nil, fmt.Errorf("nil request URL")
	}
	r2 := req.Clone(req.Context())
	r2.URL.Scheme = scheme
	r2.URL.Host = ep
	r2.Host = ep

	// Rewind body for failover retries.
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		r2.Body = body
	}
	return r2, nil
}

func ensureRewindableBody(req *http.Request) error {
	return rewindBody(req, defaultMaxRewindBodySize)
}

func rewindBody(req *http.Request, max int64) error {
	if req == nil || req.GetBody != nil || req.Body == nil || req.Body == http.NoBody {
		return nil
	}
	if max <= 0 {
		return nil
	}
	if req.ContentLength > max {
		// Body exceeds rewind buffer limit; avoid OOM by keeping stream as-is without GetBody
		return nil
	}

	orig := req.Body
	buf, err := io.ReadAll(io.LimitReader(orig, max+1))
	if err != nil {
		_ = orig.Close()
		return err
	}
	if int64(len(buf)) > max {
		// Chunked / unknown length exceeded the buffer. Keep the original
		// closer so the remainder is still readable; do not set GetBody.
		req.Body = &readCloser{Reader: io.MultiReader(bytes.NewReader(buf), orig), Closer: orig}
		return nil
	}
	_ = orig.Close()

	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(buf)), nil
	}
	req.Body, err = req.GetBody()
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(buf))
	return nil
}

type readCloser struct {
	io.Reader
	io.Closer
}

func shouldFailoverStatus(code int) bool {
	switch code {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func isFailoverError(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "TLS handshake timeout") ||
		strings.Contains(msg, "EOF")
}

func buildTransport(in buildTransportInput) http.RoundTripper {
	base := in.Base
	if base == nil {
		base = http.DefaultTransport
	}

	next := base
	if in.OTel {
		opts := []otelhttp.Option{
			otelhttp.WithTracerProvider(otel.GetTracerProvider()),
			otelhttp.WithPropagators(otel.GetTextMapPropagator()),
			otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
				if in.SpanName != "" {
					return in.SpanName
				}
				return "HTTP " + r.Method
			}),
		}
		next = otelhttp.NewTransport(base, opts...)
	}

	maxFailover := in.MaxFailover
	if maxFailover <= 0 {
		maxFailover = defaultMaxFailover
	}

	return &resolveTransport{
		pool:        in.Pool,
		scheme:      in.Scheme,
		name:        in.Name,
		maxFailover: maxFailover,
		next:        next,
	}
}

type buildTransportInput struct {
	Pool        *endpointPool
	Scheme      string
	Name        string
	Base        http.RoundTripper
	OTel        bool
	SpanName    string
	MaxFailover int
}
