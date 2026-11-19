package authz

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/fitan/fxkit/di"
	"github.com/lestrrat-go/httprc/v3"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

// TokenValidator 把原始 Bearer access token 校验为 [Subject]。
// 实现遵循 Logto 的 Go 指南：拉取 JWKS、用 key set 做 jwt.Parse，
// 再校验 issuer / audience / organization / scopes。
// 参见 https://docs.logto.io/api-protection/go/chi
type TokenValidator interface {
	// Validate 校验原始 token 并返回规范化后的 [Subject]；token 非法或过期返回错误。
	Validate(ctx context.Context, rawToken string) (Subject, error)
}

// NewValidator 在 auth.enabled 时构建基于 JWKS 的校验器；否则返回 nil。
// Casbin + DevHeaderUser 本地模式可不配置 issuer/audience（仅用 X-User 认人）。
func NewValidator(p validatorParams) (TokenValidator, error) {
	if p.Config == nil || !p.Config.Enabled {
		slog.Info("auth jwt validator disabled")
		return nil, nil
	}
	c := p.Config
	issuer := strings.TrimSpace(c.Issuer)
	audience := strings.TrimSpace(c.Audience)
	if issuer == "" || audience == "" {
		if c.Casbin.Enabled && c.DevHeaderUser {
			slog.Info("auth jwt validator skipped (casbin + dev_header_user)")
			return nil, nil
		}
		if issuer == "" {
			return nil, fmt.Errorf("auth: issuer is required when auth.enabled=true")
		}
		return nil, fmt.Errorf("auth: audience is required when auth.enabled=true")
	}
	jwksURL := strings.TrimSpace(c.JWKSURL)
	if jwksURL == "" {
		jwksURL = strings.TrimRight(issuer, "/") + "/jwks"
	}
	orgClaim := strings.TrimSpace(c.OrgClaim)

	// jwx/v3 缓存（自动刷新 JWKS）。Logto 文档用一次性的 jwk.Fetch；对稳定的
	// IdP JWKS 端点，Cache 才是生产等价物。
	// 注意：除非作为 Register 的选项传入，Register 会忽略 client 级的 HTTPClient。
	var httpClient *http.Client
	var httprcOpts []httprc.NewClientOption
	if c.TLSInsecure {
		httpClient = &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // local self-signed IdP only
			},
		}
		httprcOpts = append(httprcOpts, httprc.WithHTTPClient(httpClient))
		slog.Warn("auth jwt TLS insecure enabled (dev only)")
	}
	cache, err := jwk.NewCache(context.Background(), httprc.NewClient(httprcOpts...))
	if err != nil {
		return nil, fmt.Errorf("auth: jwks cache: %w", err)
	}
	regCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var regOpts []jwk.RegisterOption
	if httpClient != nil {
		regOpts = append(regOpts, jwk.WithHTTPClient(httpClient))
	}
	if err := cache.Register(regCtx, jwksURL, regOpts...); err != nil {
		_ = cache.Shutdown(context.Background())
		return nil, fmt.Errorf("auth: register jwks %s: %w", jwksURL, err)
	}

	slog.Info("auth jwt validator initialized",
		"issuer", issuer,
		"audience", audience,
		"jwks_url", jwksURL,
		"org_claim", orgClaim,
		"tls_insecure", c.TLSInsecure,
	)
	v := &jwksValidator{
		issuer:     issuer,
		audience:   audience,
		jwksURL:    jwksURL,
		orgClaim:   orgClaim,
		requireOrg: c.RequireOrg,
		cache:      cache,
	}
	if p.LC != nil {
		p.LC.Append(di.Hook{
			OnStop: func(ctx context.Context) error {
				return cache.Shutdown(ctx)
			},
		})
	}
	return v, nil
}

// jwksValidator 是基于缓存 JWKS 的 [TokenValidator] 实现。
type jwksValidator struct {
	// issuer 期望的 token issuer。
	issuer string
	// audience 期望的 token audience。
	audience string
	// jwksURL 是 JWKS 端点地址（默认 {issuer}/jwks）。
	jwksURL string
	// orgClaim 是写入 [Subject.OrgID] 的 claim 名。
	orgClaim string
	// requireOrg 为真时，缺少 org claim 的 token 被拒绝。
	requireOrg bool
	// cache 是 jwx 的 JWKS 缓存，会自动刷新公钥。
	cache *jwk.Cache
}

// Validate 用缓存的 JWKS 校验 token 签名、issuer 与 audience，并提取 [Subject]。
// requireOrg 为真且 token 缺少 org claim 时返回错误。
func (v *jwksValidator) Validate(ctx context.Context, rawToken string) (Subject, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return Subject{}, fmt.Errorf("auth: empty token")
	}

	set, err := v.cache.Lookup(ctx, v.jwksURL)
	recordJWKSFetch(ctx, err)
	if err != nil {
		return Subject{}, fmt.Errorf("auth: fetch jwks: %w", err)
	}

	tok, err := jwt.Parse([]byte(rawToken),
		jwt.WithKeySet(set),
		jwt.WithValidate(true),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithAcceptableSkew(30*time.Second),
	)
	if err != nil {
		return Subject{}, fmt.Errorf("auth: invalid token: %w", err)
	}

	sub, ok := tok.Subject()
	if !ok || strings.TrimSpace(sub) == "" {
		return Subject{}, fmt.Errorf("auth: token missing sub")
	}
	orgID := claimString(tok, v.orgClaim)
	if v.requireOrg && orgID == "" {
		return Subject{}, fmt.Errorf("auth: token missing org claim %q", v.orgClaim)
	}
	return Subject{
		ID:          strings.TrimSpace(sub),
		OrgID:       orgID,
		Permissions: collectPermissions(tok),
		Roles:       claimStringSlice(tok, "roles"),
	}, nil
}

// collectPermissions 从 scope/scp/permissions claim 汇总去重后的权限列表。
func collectPermissions(tok jwt.Token) []string {
	var out []string
	seen := map[string]struct{}{}
	add := func(s string) {
		for _, p := range strings.Fields(s) {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	// Logto：scopes 在 "scope" claim 中以空格分隔。
	add(claimString(tok, "scope"))
	add(claimString(tok, "scp"))
	for _, p := range claimStringSlice(tok, "permissions") {
		add(p)
	}
	for _, p := range claimStringSlice(tok, "scope") {
		add(p)
	}
	return out
}

// claimString 把 claim 读成去空白的字符串；缺失或无法转换时返回空串。
func claimString(tok jwt.Token, key string) string {
	var s string
	if err := tok.Get(key, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var v any
	if err := tok.Get(key, &v); err != nil || v == nil {
		return ""
	}
	if str, ok := v.(string); ok {
		return strings.TrimSpace(str)
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

// claimStringSlice 把 claim 读成字符串切片，支持 []string、[]any 与空格分隔字符串；缺失时返回 nil。
func claimStringSlice(tok jwt.Token, key string) []string {
	var ss []string
	if err := tok.Get(key, &ss); err == nil {
		return ss
	}
	var v any
	if err := tok.Get(key, &v); err != nil || v == nil {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case string:
		return strings.Fields(t)
	default:
		return nil
	}
}
