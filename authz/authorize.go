package authz

import (
	"context"
)

// authzDecision 是「身份已知后」的授权判定结果。
type authzDecision int

const (
	// decisionAllow 表示放行。
	decisionAllow authzDecision = iota
	// decisionForbidden 表示身份有效但无权访问（对应 403）。
	decisionForbidden
	// decisionInternal 表示判定过程出错，必须拒绝（对应 500）。
	decisionInternal
)

// authorize 在身份已经确认之后做授权。
//
// Casbin 启用时 Enforce(sub, objPath, method)。未启用时登录即可，
// 不再按 HTTPRoute.Permission 做第二套判定。
func authorize(ctx context.Context, subj Subject, method, objPath string, enf *Enforcer) authzDecision {
	if enf == nil || !enf.Enabled() {
		return decisionAllow
	}
	allowed, err := enf.Enforce(ctx, EnforceInput{Sub: subj.ID, Dom: subj.OrgID, Obj: objPath, Act: method})
	if err != nil {
		// 判定失败必须拒绝（fail closed），由调用方翻译成 500。
		return decisionInternal
	}
	if !allowed {
		return decisionForbidden
	}
	return decisionAllow
}
