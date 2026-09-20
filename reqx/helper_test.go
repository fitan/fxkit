package reqx_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fitan/fxkit/reqx"
	"github.com/imroc/req/v3"
)

type echoReq struct {
	Message string `json:"message"`
}

type echoResp struct {
	Reply string `json:"reply"`
}

func TestHTTPGenericHelpers(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			var in echoReq
			_ = json.NewDecoder(r.Body).Decode(&in)
			_ = json.NewEncoder(w).Encode(echoResp{Reply: "echo post: " + in.Message})
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(echoResp{Reply: "hello get"})
		case http.MethodPut:
			var in echoReq
			_ = json.NewDecoder(r.Body).Decode(&in)
			_ = json.NewEncoder(w).Encode(echoResp{Reply: "echo put: " + in.Message})
		case http.MethodPatch:
			var in echoReq
			_ = json.NewDecoder(r.Body).Decode(&in)
			_ = json.NewEncoder(w).Encode(echoResp{Reply: "echo patch: " + in.Message})
		case http.MethodDelete:
			_ = json.NewEncoder(w).Encode(echoResp{Reply: "hello delete"})
		default:
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
		}
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := req.C().SetBaseURL(server.URL)
	ctx := context.Background()

	// 验证 PostJSON
	postRes, err := reqx.PostJSON[echoReq, echoResp](client, ctx, "/echo", echoReq{Message: "world"})
	if err != nil || postRes.Reply != "echo post: world" {
		t.Fatalf("PostJSON failed: %v, %+v", err, postRes)
	}

	// 验证 GetJSON
	getRes, err := reqx.GetJSON[echoResp](client, ctx, "/echo")
	if err != nil || getRes.Reply != "hello get" {
		t.Fatalf("GetJSON failed: %v, %+v", err, getRes)
	}

	// 验证 PutJSON
	putRes, err := reqx.PutJSON[echoReq, echoResp](client, ctx, "/echo", echoReq{Message: "put world"})
	if err != nil || putRes.Reply != "echo put: put world" {
		t.Fatalf("PutJSON failed: %v, %+v", err, putRes)
	}

	// 验证 PatchJSON
	patchRes, err := reqx.PatchJSON[echoReq, echoResp](client, ctx, "/echo", echoReq{Message: "patch world"})
	if err != nil || patchRes.Reply != "echo patch: patch world" {
		t.Fatalf("PatchJSON failed: %v, %+v", err, patchRes)
	}

	// 验证 DeleteJSON
	delRes, err := reqx.DeleteJSON[echoResp](client, ctx, "/echo")
	if err != nil || delRes.Reply != "hello delete" {
		t.Fatalf("DeleteJSON failed: %v, %+v", err, delRes)
	}
}
