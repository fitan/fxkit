package authz

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fitan/fxkit/di"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/stretchr/testify/require"
)

// TestValidatorAgainstLocalJWKS 真实走一遍「JWKS 拉取 → 验签 → issuer/audience/org 校验」，
// 用本地 JWKS 服务 + 真实 RSA 签名，覆盖 NewValidator 的完整路径。
func TestValidatorAgainstLocalJWKS(t *testing.T) {
	raw, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	priv, err := jwk.Import(raw)
	require.NoError(t, err)
	require.NoError(t, priv.Set(jwk.KeyIDKey, "kid-1"))
	require.NoError(t, priv.Set(jwk.AlgorithmKey, jwa.RS256()))

	pub, err := jwk.PublicKeyOf(raw)
	require.NoError(t, err)
	require.NoError(t, pub.Set(jwk.KeyIDKey, "kid-1"))
	require.NoError(t, pub.Set(jwk.AlgorithmKey, jwa.RS256()))

	set := jwk.NewSet()
	require.NoError(t, set.AddKey(pub))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(set)
	}))
	defer srv.Close()

	const issuer = "https://issuer.itest"
	const audience = "api://itest"
	cfg := &Config{
		Enabled:    true,
		Issuer:     issuer,
		Audience:   audience,
		JWKSURL:    srv.URL,
		OrgClaim:   "organization_id",
		RequireOrg: true,
	}

	lc := di.NewLifecycle()
	v, err := NewValidator(validatorParams{Config: cfg, LC: lc})
	require.NoError(t, err)
	t.Cleanup(func() { _ = lc.Stop(context.Background()) })
	require.NotNil(t, v)

	sign := func(build func(*jwt.Builder) *jwt.Builder) string {
		b := jwt.NewBuilder().
			Issuer(issuer).
			Subject("alice").
			Expiration(time.Now().Add(time.Hour))
		b = build(b)
		tok, err := b.Build()
		require.NoError(t, err)
		signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256(), priv))
		require.NoError(t, err)
		return string(signed)
	}

	// 正确 token
	token := sign(func(b *jwt.Builder) *jwt.Builder {
		return b.Audience([]string{audience}).Claim("organization_id", "org-1")
	})
	subj, err := v.Validate(context.Background(), token)
	require.NoError(t, err)
	require.Equal(t, "alice", subj.ID)
	require.Equal(t, "org-1", subj.OrgID)

	// audience 不匹配
	badAud := sign(func(b *jwt.Builder) *jwt.Builder {
		return b.Audience([]string{"api://other"}).Claim("organization_id", "org-1")
	})
	_, err = v.Validate(context.Background(), badAud)
	require.Error(t, err)

	// 缺少 org claim（require_org=true）
	noOrg := sign(func(b *jwt.Builder) *jwt.Builder {
		return b.Audience([]string{audience})
	})
	_, err = v.Validate(context.Background(), noOrg)
	require.Error(t, err)
}
