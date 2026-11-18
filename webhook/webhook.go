// Package webhook 提供 webhook 签名校验与原始 body 读取。
//
// 只做验签/时间戳/原始 body；事件去重请用 outbox/inbox（按 provider event ID）。
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultSignatureHeader = "X-Webhook-Signature"
	DefaultTimestampHeader = "X-Webhook-Timestamp"
	defaultTolerance       = 5 * time.Minute
	defaultMaxBodyBytes    = 1 << 20
)

// Verifier 校验 webhook 的 HMAC-SHA256 签名与时间戳。
type Verifier struct {
	Secret          []byte
	SignatureHeader string
	TimestampHeader string
	Tolerance       time.Duration
	Now             func() time.Time
}

// Sign 返回 HMAC-SHA256 的 hex 签名。
func Sign(secret, body []byte) string {
	h := hmac.New(sha256.New, secret)
	_, _ = h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// Verify 校验签名与时间戳。timestamp 为 Unix 秒。
func (v Verifier) Verify(body []byte, signature, timestamp string) error {
	if len(v.Secret) == 0 {
		return fmt.Errorf("webhook: secret is required")
	}
	sig := strings.TrimSpace(signature)
	sig = strings.TrimPrefix(sig, "sha256=")
	expected := Sign(v.Secret, body)
	if subtle.ConstantTimeCompare([]byte(sig), []byte(expected)) != 1 {
		return fmt.Errorf("webhook: invalid signature")
	}

	ts := strings.TrimSpace(timestamp)
	if ts == "" {
		return fmt.Errorf("webhook: timestamp is required")
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("webhook: invalid timestamp")
	}
	now := time.Now
	if v.Now != nil {
		now = v.Now
	}
	tol := v.Tolerance
	if tol <= 0 {
		tol = defaultTolerance
	}
	if d := now().Sub(time.Unix(sec, 0)); d > tol || d < -tol {
		return fmt.Errorf("webhook: timestamp outside tolerance")
	}
	return nil
}

// VerifyRequest 从请求头读取签名/时间戳并校验。
func (v Verifier) VerifyRequest(r *http.Request, body []byte) error {
	if r == nil {
		return fmt.Errorf("webhook: nil request")
	}
	sigHeader := v.SignatureHeader
	if sigHeader == "" {
		sigHeader = DefaultSignatureHeader
	}
	tsHeader := v.TimestampHeader
	if tsHeader == "" {
		tsHeader = DefaultTimestampHeader
	}
	return v.Verify(body, r.Header.Get(sigHeader), r.Header.Get(tsHeader))
}

// ReadBody 读取并恢复请求体，返回原始字节（签名校验必须用原始 body）。
func ReadBody(r *http.Request, limit int64) ([]byte, error) {
	if r == nil || r.Body == nil || r.Body == http.NoBody {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultMaxBodyBytes
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(io.MultiReader(strings.NewReader(string(raw)), r.Body))
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("webhook: body exceeds %d bytes", limit)
	}
	return raw, nil
}
