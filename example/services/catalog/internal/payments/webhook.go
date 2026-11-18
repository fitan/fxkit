package payments

import (
	"encoding/json"
	"net/http"

	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/server"
	"github.com/fitan/fxkit/webhook"
)

// webhookRoutes 把渠道回调挂到裸 chi；它需要原始 body 验签，不适合 Huma 自动反序列化。
func webhookRoutes(svc *Service) []server.Route {
	return []server.Route{{
		Pattern: "/payments/webhook",
		Methods: []string{http.MethodPost},
		Handler: http.HandlerFunc(svc.handleWebhook),
	}}
}

// handleWebhook 的顺序很重要：
//  1. 读取并恢复原始 body；
//  2. 先验 HMAC 签名与时间戳，再反序列化；
//  3. 交给 Service 做 inbox 去重 + 状态机 + 账本。
//
// 先反序列化再验签会让未认证的请求进入业务逻辑。
func (s *Service) handleWebhook(w http.ResponseWriter, r *http.Request) {
	raw, err := webhook.ReadBody(r, 1<<20)
	if err != nil {
		fxerrors.WriteError(w, r, fxerrors.BadRequest("read webhook body"))
		return
	}
	verifier := webhook.Verifier{Secret: []byte(s.cfg.WebhookSecret)}
	if err := verifier.VerifyRequest(r, raw); err != nil {
		fxerrors.WriteError(w, r, fxerrors.Unauthorized("invalid webhook signature"))
		return
	}
	var ev WebhookEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		fxerrors.WriteError(w, r, fxerrors.BadRequest("invalid webhook json"))
		return
	}
	d, err := s.HandleWebhook(r.Context(), ev)
	if err != nil {
		fxerrors.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(d)
}
