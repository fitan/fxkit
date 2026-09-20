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

func TestPostJSON_and_GetJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var in echoReq
			_ = json.NewDecoder(r.Body).Decode(&in)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(echoResp{Reply: "echo: " + in.Message})
			return
		}
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(echoResp{Reply: "hello get"})
			return
		}
		http.Error(w, "bad method", http.StatusMethodNotAllowed)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := req.C().SetBaseURL(server.URL)
	ctx := context.Background()

	// 验证 PostJSON
	postRes, err := reqx.PostJSON[echoReq, echoResp](client, ctx, "/echo", echoReq{Message: "world"})
	if err != nil {
		t.Fatalf("PostJSON failed: %v", err)
	}
	if postRes.Reply != "echo: world" {
		t.Fatalf("unexpected PostJSON reply: %s", postRes.Reply)
	}

	// 验证 GetJSON
	getRes, err := reqx.GetJSON[echoResp](client, ctx, "/echo")
	if err != nil {
		t.Fatalf("GetJSON failed: %v", err)
	}
	if getRes.Reply != "hello get" {
		t.Fatalf("unexpected GetJSON reply: %s", getRes.Reply)
	}
}
