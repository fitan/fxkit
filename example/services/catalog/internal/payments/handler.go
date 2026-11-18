package payments

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/authz"
	fxhuma "github.com/fitan/fxkit/huma"
)

// listIn 是支付列表查询参数。
type listIn struct {
	// Limit 是最多返回条数。
	Limit int `query:"limit"`
}

// listOut 是支付列表响应。
type listOut struct {
	Body struct {
		// Items 是支付单列表。
		Items []PaymentDetail `json:"items"`
	}
}

// createIn 是创建支付单的请求包装。
type createIn struct {
	Body CreatePaymentReq
}

// detailOut 是所有支付单接口共用的响应包装。
type detailOut struct {
	Body PaymentDetail
}

// idPath 是带 {id} 路径参数的输入。
type idPath struct {
	// ID 是支付单 ID。
	ID string `path:"id"`
}

// ledgerOut 包一层 Items，方便后续加分页/合计字段。
// refundIn 支持可选金额；省略即退剩余可退金额。
type refundIn struct {
	// ID 是支付单 ID。
	ID   string `path:"id"`
	Body RefundReq
}

type ledgerOut struct {
	Body struct {
		// Items 是账本分录。
		Items []LedgerEntry `json:"items"`
	}
}

// reconcileOut 的 Summary 按差异类型计数，便于监控告警。
type reconcileOut struct {
	Body reconcileBody
}

var (
	listPaymentsOp = huma.Operation{
		OperationID: "listPayments",
		Method:      http.MethodGet,
		Path:        "/payments",
		Summary:     "List recent payments",
		Tags:        []string{"Payments"},
	}
	createPaymentOp = huma.Operation{
		OperationID: "createPayment",
		Method:      http.MethodPost,
		Path:        "/payments",
		Summary:     "Create payment (use Idempotency-Key for retries)",
		Tags:        []string{"Payments"},
	}
	getPaymentOp = huma.Operation{
		OperationID: "getPayment",
		Method:      http.MethodGet,
		Path:        "/payments/{id}",
		Summary:     "Get payment",
		Tags:        []string{"Payments"},
	}
	authorizePaymentOp = huma.Operation{
		OperationID: "authorizePayment",
		Method:      http.MethodPost,
		Path:        "/payments/{id}/authorize",
		Summary:     "Authorize payment (CAS state machine)",
		Tags:        []string{"Payments"},
	}
	capturePaymentOp = huma.Operation{
		OperationID: "capturePayment",
		Method:      http.MethodPost,
		Path:        "/payments/{id}/capture",
		Summary:     "Capture payment (writes double-entry ledger)",
		Tags:        []string{"Payments"},
	}
	refundPaymentOp = huma.Operation{
		OperationID: "refundPayment",
		Method:      http.MethodPost,
		Path:        "/payments/{id}/refund",
		Summary:     "Refund payment (reverses ledger)",
		Tags:        []string{"Payments"},
	}
	paymentLedgerOp = huma.Operation{
		OperationID: "getPaymentLedger",
		Method:      http.MethodGet,
		Path:        "/payments/{id}/ledger",
		Summary:     "Payment ledger postings",
		Tags:        []string{"Payments"},
	}
	reconcilePaymentsOp = huma.Operation{
		OperationID: "reconcilePayments",
		Method:      http.MethodPost,
		Path:        "/payments/reconcile",
		Summary:     "Compare internal payments with provider settlements",
		Tags:        []string{"Payments"},
	}
)

// Register 注册支付主流程与对账接口；渠道 webhook 是裸 chi 路由，见 webhook.go。
func Register(svc *Service) fxhuma.Registrar {
	return fxhuma.FuncRegistrar(func(api huma.API) {
		fxhuma.Register(api, listPaymentsOp, func(ctx context.Context, in *listIn) (*listOut, error) {
			items, err := svc.List(ctx, in.Limit)
			if err != nil {
				return nil, err
			}
			out := &listOut{}
			out.Body.Items = items
			return out, nil
		})
		fxhuma.Register(api, createPaymentOp, func(ctx context.Context, in *createIn) (*detailOut, error) {
			d, err := svc.Create(ctx, in.Body)
			if err != nil {
				return nil, err
			}
			return &detailOut{Body: d}, nil
		})
		fxhuma.Register(api, getPaymentOp, func(ctx context.Context, in *idPath) (*detailOut, error) {
			d, err := svc.Get(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			return &detailOut{Body: d}, nil
		})
		fxhuma.Register(api, authorizePaymentOp, func(ctx context.Context, in *idPath) (*detailOut, error) {
			d, err := svc.Authorize(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			return &detailOut{Body: d}, nil
		})
		fxhuma.Register(api, capturePaymentOp, func(ctx context.Context, in *idPath) (*detailOut, error) {
			d, err := svc.Capture(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			return &detailOut{Body: d}, nil
		})
		fxhuma.Register(api, refundPaymentOp, func(ctx context.Context, in *refundIn) (*detailOut, error) {
			d, err := svc.Refund(ctx, in.ID, in.Body)
			if err != nil {
				return nil, err
			}
			return &detailOut{Body: d}, nil
		})
		fxhuma.Register(api, paymentLedgerOp, func(ctx context.Context, in *idPath) (*ledgerOut, error) {
			items, err := svc.LedgerEntries(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			out := &ledgerOut{}
			out.Body.Items = items
			return out, nil
		})
		fxhuma.Register(api, reconcilePaymentsOp, func(ctx context.Context, _ *struct{}) (*reconcileOut, error) {
			diffs, err := svc.Reconcile(ctx)
			if err != nil {
				return nil, err
			}
			summary := make(map[string]int)
			for _, d := range diffs {
				summary[d.Kind]++
			}
			return &reconcileOut{Body: reconcileBody{Diffs: diffs, Summary: summary}}, nil
		})
	})
}

// paymentHTTPRoutes 把 Huma operation 与裸 chi webhook 一起登记到 authz 目录。
// webhook 不走 Huma，必须单独加一条 HTTPRoute，否则 Casbin 下拿不到策略。
func paymentHTTPRoutes() []authz.HTTPRoute {
	routes := authz.HTTPRoutesFromOperations(
		listPaymentsOp, createPaymentOp, getPaymentOp, authorizePaymentOp, capturePaymentOp,
		refundPaymentOp, paymentLedgerOp, reconcilePaymentsOp,
	)
	return append(routes, authz.HTTPRoute{
		Method:      http.MethodPost,
		Path:        "/payments/webhook",
		OperationID: "paymentWebhook",
		Summary:     "Provider webhook (HMAC verified)",
	})
}
