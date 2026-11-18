package webhook_test

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fitan/fxkit/webhook"
)

func TestSignAndVerify(t *testing.T) {
	body := []byte(`{"id":"evt_1"}`)
	secret := []byte("secret")
	sig := webhook.Sign(secret, body)
	now := time.Unix(123, 0)
	v := webhook.Verifier{Secret: secret, Now: func() time.Time { return now }}
	if err := v.Verify(body, sig, "123"); err != nil {
		t.Fatalf("verify=%v", err)
	}
	if err := v.Verify(body, "bad", "123"); err == nil {
		t.Fatal("expected invalid signature")
	}
}

func TestVerifyTimestampTolerance(t *testing.T) {
	body := []byte(`{}`)
	secret := []byte("secret")
	sig := webhook.Sign(secret, body)
	now := time.Unix(1_700_000_000, 0)
	v := webhook.Verifier{Secret: secret, Tolerance: time.Minute, Now: func() time.Time { return now }}
	if err := v.Verify(body, sig, "1700000000"); err != nil {
		t.Fatalf("fresh timestamp: %v", err)
	}
	if err := v.Verify(body, sig, "1699999000"); err == nil {
		t.Fatal("expected stale timestamp")
	}
}

func TestReadBodyRestores(t *testing.T) {
	r := httptest.NewRequest("POST", "/webhook", strings.NewReader(`{"a":1}`))
	body, err := webhook.ReadBody(r, 1024)
	if err != nil || string(body) != `{"a":1}` {
		t.Fatalf("body=%q err=%v", body, err)
	}
	rest, err := io.ReadAll(r.Body)
	if err != nil || string(rest) != `{"a":1}` {
		t.Fatalf("restored=%q err=%v", rest, err)
	}
}
