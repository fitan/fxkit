package fxerrors_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fitan/fxkit/fxerrors"
)

// TestErrorJSONDerivesStatus 是回归测试：
// Status 曾经是结构体上的可写字段，于是必须到处防「手写 &Error{} 导致的零值」，
// 而且 code 与 status 可能不一致。现在 status 由 Kind 派生并在序列化时输出。
func TestErrorJSONDerivesStatus(t *testing.T) {
	err := fxerrors.Validation(map[string]string{"email": "invalid"})

	raw, marshalErr := json.Marshal(err)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	var got struct {
		Code   string `json:"code"`
		Status int    `json:"status"`
	}
	if unmarshalErr := json.Unmarshal(raw, &got); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	if got.Status != http.StatusBadRequest {
		t.Fatalf("status=%d, want %d (raw=%s)", got.Status, http.StatusBadRequest, raw)
	}
	if got.Code != string(fxerrors.KindValidation) {
		t.Fatalf("code=%q, want %q", got.Code, fxerrors.KindValidation)
	}
}

// TestWriteErrorStatusMatchesKind 确认响应码与序列化出的 status 一致。
func TestWriteErrorStatusMatchesKind(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/things/42", nil)

	fxerrors.WriteError(rec, req, fxerrors.NotFound("thing", "42"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("http status=%d, want 404", rec.Code)
	}
	var body struct {
		Status int `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != http.StatusNotFound {
		t.Fatalf("body status=%d, want 404", body.Status)
	}
}
